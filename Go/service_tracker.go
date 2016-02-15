// Copyright 2016 Asya Labs
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package etcd_recipes

import (
	"sync"

	"github.com/coreos/etcd/Godeps/_workspace/src/golang.org/x/net/context"
	"github.com/coreos/etcd/client"
)

// SERVICE-TRACKER RECIPE
//
// This recipe can be used to discover the instances that make up a distributed
// service. The instances can be running anywhere in the cluster. The following
// is how it works:
//   - All the instances that make up a distributed service create an ephemeral
//     key under a well known directory in the etcd namespace. The ephemeral keys
//     provide the liveness tracking capability. So, if an instance dies then its
//     corresponding key also disappears. The value stored in the ephemral key
//     can provide details of the instance like IP address/port number etc...
//   - The interested parties will instantiate a ServiceTracker recipe which
//     observes the well known path to identify the instances that make up the
//     service in question. The ServiceTracker recipe makes use of the Observer
//     recipe to track changes.
//   - The ServiceTracker notifies the caller of any change in the directory by
//     sending a list of all the instances that are present under the directory.

// A descriptor structure for the service tracking operation.
type ServiceTracker struct {
	// Pointer to the etcd connection descriptor.
	ec *EtcdConnector

	// Path under which the service instances will be tracked.
	servicePath string

	// Observer instance to monitor the changes.
	obsvr *Observer

	// WaitGroup instance used to wait for the go-routine to exit.
	wg *sync.WaitGroup
}

// A structure that describes a key-value pair.
type Pair struct {
	Key   string
	Value string
}

// A structure that will be sent back to the caller whenever a change
// is observed under @servicePath.
type TrackerData struct {
	// An array of all active service instances represented as key-value pairs.
	Pairs []Pair

	// Error information, if any.
	Err error
}

// Description:
//     A constructor routine to instantiate a service tracking operation.
//
// Parameters:
//     @path - A path in the etcd namespace under which the instances will
//             be tracked.
//
// Return value:
//     1. A pointer to the ServiceTracker instance.
func (ec *EtcdConnector) NewServiceTracker(path string) *ServiceTracker {
	st := &ServiceTracker{
		ec:          ec,
		servicePath: path,
		obsvr:       ec.NewObserver(path),
		wg:          &sync.WaitGroup{},
	}
	return st
}

// Description:
//     A routine to start the service tracking operation. This routine starts
//     an Observer on @servicePath and waits to hear from the Observer about
//     changes. If any change is seen then it compares the current array of
//     Pairs with a cached version. If any difference is seen at all then it
//     posts the current Pairs on the outward channel.
//
// Parameters:
//     None
//
// Return value:
//     1. A channel on which TrackerData will be notified.
func (st *ServiceTracker) Start() (<-chan TrackerData, error) {
	// Create an outward channel on which service tracker info will be sent.
	tracker := make(chan TrackerData, 2)

	// Start the Observer.
	obResp, err := st.obsvr.Start(0, true)
	if err != nil {
		close(tracker)
		return nil, err
	}

	var curKeyVals []Pair
	opts := &client.GetOptions{Sort: true, Recursive: true}

	// Account for the go-routine in WaitGroup.
	st.wg.Add(1)

	// Observe the changes in a go routine.
	go func() {
		for or := range obResp {
			// For every trigger check if anything has changed.
			updated := false

			// If any error, report it back to the caller. Rely on the caller
			// to handle the error appropriately.
			if or.Err != nil {
				tracker <- TrackerData{Pairs: nil, Err: or.Err}
				continue
			}

			var newKeyVals []Pair

			// Get the latest contents of @servicePath directory.
			r, e := st.ec.Get(context.Background(), st.servicePath, opts)
			if e != nil {
				tracker <- TrackerData{Pairs: nil, Err: e}
				continue
			}

			// Construct the new key-value pairs found under @servicePath
			for i := 0; i < len(r.Node.Nodes); i++ {
				newKeyVals = append(newKeyVals, Pair{
					Key:   r.Node.Nodes[i].Key,
					Value: r.Node.Nodes[i].Value,
				})
			}

			// Perform a diff.
			if curKeyVals == nil || len(curKeyVals) != len(newKeyVals) {
				curKeyVals = newKeyVals
				updated = true
			} else {
				for i := 0; i < len(newKeyVals); i++ {
					if curKeyVals[i].Key != newKeyVals[i].Key ||
						curKeyVals[i].Value != newKeyVals[i].Value {
						curKeyVals = newKeyVals
						updated = true
						break
					}
				}
			}

			// if anything has changed then send the new pairs to the caller.
			if updated == true {
				tracker <- TrackerData{Pairs: curKeyVals, Err: nil}
			}
		}

		// If the observer channel is closed then close the tracker channel too.
		close(tracker)

		// Adjust the WaitGroup counter before exiting the go-routine.
		st.wg.Done()
	}()

	return tracker, nil
}

// Description:
//     A routine to stop the service tracking openation.
//
// Parameters:
//     None
//
// Return value:
//     None
func (st *ServiceTracker) Stop() {
	st.obsvr.Stop()
	st.wg.Wait()
}
