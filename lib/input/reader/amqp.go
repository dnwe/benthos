// Copyright (c) 2018 Ashley Jeffs
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package reader

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Jeffail/benthos/lib/log"
	"github.com/Jeffail/benthos/lib/metrics"
	"github.com/Jeffail/benthos/lib/types"
	"github.com/streadway/amqp"
)

//------------------------------------------------------------------------------

// AMQPConfig is configuration for the AMQP input type.
type AMQPConfig struct {
	URL           string `json:"url" yaml:"url"`
	Exchange      string `json:"exchange" yaml:"exchange"`
	ExchangeType  string `json:"exchange_type" yaml:"exchange_type"`
	Queue         string `json:"queue" yaml:"queue"`
	BindingKey    string `json:"key" yaml:"key"`
	ConsumerTag   string `json:"consumer_tag" yaml:"consumer_tag"`
	PrefetchCount int    `json:"prefetch_count" yaml:"prefetch_count"`
	PrefetchSize  int    `json:"prefetch_size" yaml:"prefetch_size"`
}

// NewAMQPConfig creates a new AMQPConfig with default values.
func NewAMQPConfig() AMQPConfig {
	return AMQPConfig{
		URL:           "amqp://guest:guest@localhost:5672/",
		Exchange:      "benthos-exchange",
		ExchangeType:  "direct",
		Queue:         "benthos-queue",
		BindingKey:    "benthos-key",
		ConsumerTag:   "benthos-consumer",
		PrefetchCount: 10,
		PrefetchSize:  0,
	}
}

//------------------------------------------------------------------------------

// AMQP is an input type that reads messages via the AMQP 0.91 protocol.
type AMQP struct {
	conn         *amqp.Connection
	amqpChan     *amqp.Channel
	consumerChan <-chan amqp.Delivery

	ackTag uint64

	conf  AMQPConfig
	stats metrics.Type
	log   log.Modular

	m sync.RWMutex
}

// NewAMQP creates a new AMQP input type.
func NewAMQP(conf AMQPConfig, log log.Modular, stats metrics.Type) (Type, error) {
	a := AMQP{
		conf:  conf,
		stats: stats,
		log:   log.NewModule(".input.amqp"),
	}
	return &a, nil
}

//------------------------------------------------------------------------------

// Connect establishes a connection to an AMQP server.
func (a *AMQP) Connect() (err error) {
	a.m.Lock()
	defer a.m.Unlock()

	if a.conn != nil {
		return nil
	}

	var conn *amqp.Connection
	var amqpChan *amqp.Channel
	var consumerChan <-chan amqp.Delivery

	conn, err = amqp.Dial(a.conf.URL)
	if err != nil {
		return fmt.Errorf("AMQP Connect: %s", err)
	}

	amqpChan, err = conn.Channel()
	if err != nil {
		return fmt.Errorf("AMQP Channel: %s", err)
	}

	if err = amqpChan.ExchangeDeclare(
		a.conf.Exchange,     // name of the exchange
		a.conf.ExchangeType, // type
		true,                // durable
		false,               // delete when complete
		false,               // internal
		false,               // noWait
		nil,                 // arguments
	); err != nil {
		return fmt.Errorf("Exchange Declare: %s", err)
	}

	if _, err = amqpChan.QueueDeclare(
		a.conf.Queue, // name of the queue
		true,         // durable
		false,        // delete when usused
		false,        // exclusive
		false,        // noWait
		nil,          // arguments
	); err != nil {
		return fmt.Errorf("Queue Declare: %s", err)
	}

	if err = amqpChan.QueueBind(
		a.conf.Queue,      // name of the queue
		a.conf.BindingKey, // bindingKey
		a.conf.Exchange,   // sourceExchange
		false,             // noWait
		nil,               // arguments
	); err != nil {
		return fmt.Errorf("Queue Bind: %s", err)
	}

	if err = amqpChan.Qos(
		a.conf.PrefetchCount, a.conf.PrefetchSize, false,
	); err != nil {
		return fmt.Errorf("Qos: %s", err)
	}

	if consumerChan, err = amqpChan.Consume(
		a.conf.Queue,       // name
		a.conf.ConsumerTag, // consumerTag,
		false,              // noAck
		false,              // exclusive
		false,              // noLocal
		false,              // noWait
		nil,                // arguments
	); err != nil {
		return fmt.Errorf("Queue Consume: %s", err)
	}

	a.conn = conn
	a.amqpChan = amqpChan
	a.consumerChan = consumerChan

	a.log.Infof("Receiving AMQP messages from URL: %s\n", a.conf.URL)
	return
}

// disconnect safely closes a connection to an AMQP server.
func (a *AMQP) disconnect() error {
	a.m.Lock()
	defer a.m.Unlock()

	if a.amqpChan != nil {
		err := a.amqpChan.Cancel(a.conf.ConsumerTag, true)
		a.amqpChan = nil
		if err != nil {
			return fmt.Errorf("Consumer cancel failed: %s", err)
		}
	}
	if a.conn != nil {
		err := a.conn.Close()
		a.conn = nil
		if err != nil {
			return fmt.Errorf("AMQP connection close error: %s", err)
		}
	}

	return nil
}

//------------------------------------------------------------------------------

// Message is a wrapper for types.Message so we can dynamically add metadata.
type Message struct {
	m types.Message
}

// Determine the type of the value and set the metadata.
func (m *Message) setMetadata(k string, v interface{}) {
	var metaValue string
	var metaKey = strings.Replace(k, "-", "_", -1)

	switch v := v.(type) {
	case bool:
		metaValue = strconv.FormatBool(bool(v))
	case float32:
		metaValue = strconv.FormatFloat(float64(v), 'f', -1, 32)
	case float64:
		metaValue = strconv.FormatFloat(v, 'f', -1, 64)
	case byte:
		metaValue = strconv.Itoa(int(v))
	case int16:
		metaValue = strconv.Itoa(int(v))
	case int32:
		metaValue = strconv.Itoa(int(v))
	case int64:
		metaValue = strconv.Itoa(int(v))
	case nil:
		metaValue = ""
	case string:
		metaValue = v
	case []byte:
		metaValue = string(v[:])
	case time.Time:
		metaValue = v.Format(time.RFC3339)
	case amqp.Decimal:
		dec := strconv.Itoa(int(v.Value))
		index := len(dec) - int(v.Scale)
		metaValue = dec[:index] + "." + dec[index:]
	case amqp.Table:
		for key, value := range v {
			m.setMetadata(metaKey+"_"+key, value)
		}
		return
	default:
		metaValue = ""
	}

	if metaValue != "" {
		m.m.SetMetadata(metaKey, metaValue)
	}
}

// Add metadata from the AMQP headers.
func (m *Message) amqpHeaders(t amqp.Table) {
	for k, v := range t {
		m.setMetadata("amqp_headers_"+k, v)
	}
}

//------------------------------------------------------------------------------

// Read a new AMQP message.
func (a *AMQP) Read() (types.Message, error) {
	var c <-chan amqp.Delivery

	a.m.RLock()
	if a.conn != nil {
		c = a.consumerChan
	}
	a.m.RUnlock()

	if c == nil {
		return nil, types.ErrNotConnected
	}

	data, open := <-c
	if !open {
		a.disconnect()
		return nil, types.ErrNotConnected
	}

	// Only store the latest delivery tag, but always Ack multiple.
	a.ackTag = data.DeliveryTag

	msg := Message{}
	msg.m = types.NewMessage([][]byte{data.Body})

	msg.amqpHeaders(data.Headers)

	msg.setMetadata("amqp_content_type", data.ContentType)
	msg.setMetadata("amqp_content_encoding", data.ContentEncoding)

	if data.DeliveryMode != 0 {
		msg.setMetadata("amqp_delivery_mode", data.DeliveryMode)
	}

	msg.setMetadata("amqp_priority", data.Priority)
	msg.setMetadata("amqp_correlation_id", data.CorrelationId)
	msg.setMetadata("amqp_reply_to", data.ReplyTo)
	msg.setMetadata("amqp_expiration", data.Expiration)
	msg.setMetadata("amqp_message_id", data.MessageId)

	if !data.Timestamp.IsZero() {
		msg.setMetadata("amqp_timestamp", data.Timestamp.Unix())
	}

	msg.setMetadata("amqp_type", data.Type)
	msg.setMetadata("amqp_user_id", data.UserId)
	msg.setMetadata("amqp_app_id", data.AppId)
	msg.setMetadata("amqp_consumer_tag", data.ConsumerTag)
	msg.setMetadata("amqp_delivery_tag", data.DeliveryTag)
	msg.setMetadata("amqp_redelivered", data.Redelivered)
	msg.setMetadata("amqp_exchange", data.Exchange)
	msg.setMetadata("amqp_routing_key", data.RoutingKey)

	return msg.m, nil
}

// Acknowledge instructs whether unacknowledged messages have been successfully
// propagated.
func (a *AMQP) Acknowledge(err error) error {
	a.m.RLock()
	defer a.m.RUnlock()
	if a.conn == nil {
		return types.ErrNotConnected
	}
	if err != nil {
		return a.amqpChan.Reject(a.ackTag, true)
	}
	return a.amqpChan.Ack(a.ackTag, true)
}

// CloseAsync shuts down the AMQP input and stops processing requests.
func (a *AMQP) CloseAsync() {
	a.disconnect()
}

// WaitForClose blocks until the AMQP input has closed down.
func (a *AMQP) WaitForClose(timeout time.Duration) error {
	return nil
}

//------------------------------------------------------------------------------
