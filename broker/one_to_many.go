/*
Copyright (c) 2014 Ashley Jeffs

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
THE SOFTWARE.
*/

package broker

import (
	"time"

	"github.com/jeffail/benthos/output"
	"github.com/jeffail/benthos/types"
)

//--------------------------------------------------------------------------------------------------

// OneToMany - A one-to-many broker type.
type OneToMany struct {
	messages     <-chan types.Message
	responseChan chan Response

	outputsChan chan []output.Type
	outputs     []output.Type

	closedChan chan struct{}
	closeChan  chan struct{}
}

//--------------------------------------------------------------------------------------------------

// SetOutputs - Set the broker outputs.
func (o *OneToMany) SetOutputs(outs []output.Type) {
	o.outputsChan <- outs
}

//--------------------------------------------------------------------------------------------------

// loop - Internal loop brokers incoming messages to many outputs.
func (o *OneToMany) loop() {
	running := true
	for running {
		select {
		case _ = <-o.messages:
			// TODO
			o.responseChan <- Response{}
		case outs := <-o.outputsChan:
			o.outputs = outs
		case _, running = <-o.closeChan:
			running = false
		}
	}

	close(o.responseChan)
	close(o.closedChan)
}

// ResponseChan - Returns the response channel.
func (o *OneToMany) ResponseChan() <-chan Response {
	return o.responseChan
}

// CloseAsync - Shuts down the OneToMany broker and stops processing requests.
func (o *OneToMany) CloseAsync() {
	close(o.closeChan)
	close(o.outputsChan)
}

// WaitForClose - Blocks until the OneToMany broker has closed down.
func (o *OneToMany) WaitForClose(timeout time.Duration) error {
	select {
	case <-o.closedChan:
	case <-time.After(timeout):
		return types.ErrTimeout
	}
	return nil
}

//--------------------------------------------------------------------------------------------------
