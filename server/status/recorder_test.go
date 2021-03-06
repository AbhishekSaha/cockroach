// Copyright 2015 The Cockroach Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
// implied. See the License for the specific language governing
// permissions and limitations under the License.
//
// Author: Matt Tracy (matt.r.tracy@gmail.com)

package status

import (
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"testing"

	"github.com/kr/pretty"

	"github.com/cockroachdb/cockroach/roachpb"
	"github.com/cockroachdb/cockroach/storage"
	"github.com/cockroachdb/cockroach/storage/engine"
	"github.com/cockroachdb/cockroach/ts"
	"github.com/cockroachdb/cockroach/util/hlc"
	"github.com/cockroachdb/cockroach/util/leaktest"
	"github.com/cockroachdb/cockroach/util/metric"
)

// byTimeAndName is a slice of ts.TimeSeriesData.
type byTimeAndName []ts.TimeSeriesData

// implement sort.Interface for byTimeAndName
func (a byTimeAndName) Len() int      { return len(a) }
func (a byTimeAndName) Swap(i, j int) { a[i], a[j] = a[j], a[i] }
func (a byTimeAndName) Less(i, j int) bool {
	if a[i].Name != a[j].Name {
		return a[i].Name < a[j].Name
	}
	if a[i].Datapoints[0].TimestampNanos != a[j].Datapoints[0].TimestampNanos {
		return a[i].Datapoints[0].TimestampNanos < a[j].Datapoints[0].TimestampNanos
	}
	return a[i].Source < a[j].Source
}

var _ sort.Interface = byTimeAndName{}

// byStoreID is a slice of roachpb.StoreID.
type byStoreID []roachpb.StoreID

// implement sort.Interface for byStoreID
func (a byStoreID) Len() int      { return len(a) }
func (a byStoreID) Swap(i, j int) { a[i], a[j] = a[j], a[i] }
func (a byStoreID) Less(i, j int) bool {
	return a[i] < a[j]
}

var _ sort.Interface = byStoreID{}

// byStoreDescID is a slice of storage.StoreStatus
type byStoreDescID []storage.StoreStatus

// implement sort.Interface for byStoreDescID.
func (a byStoreDescID) Len() int      { return len(a) }
func (a byStoreDescID) Swap(i, j int) { a[i], a[j] = a[j], a[i] }
func (a byStoreDescID) Less(i, j int) bool {
	return a[i].Desc.StoreID < a[j].Desc.StoreID
}

var _ sort.Interface = byStoreDescID{}

// TestNodeStatusRecorder verifies that the time series data generated by a
// recorder matches the data added to the monitor.
func TestNodeStatusRecorder(t *testing.T) {
	defer leaktest.AfterTest(t)
	nodeDesc := roachpb.NodeDescriptor{
		NodeID: roachpb.NodeID(1),
	}
	storeDesc1 := roachpb.StoreDescriptor{
		StoreID: roachpb.StoreID(1),
		Capacity: roachpb.StoreCapacity{
			Capacity:  100,
			Available: 50,
		},
	}
	storeDesc2 := roachpb.StoreDescriptor{
		StoreID: roachpb.StoreID(2),
		Capacity: roachpb.StoreCapacity{
			Capacity:  200,
			Available: 75,
		},
	}
	desc1 := &roachpb.RangeDescriptor{
		RangeID:  1,
		StartKey: roachpb.RKey("a"),
		EndKey:   roachpb.RKey("b"),
	}
	desc2 := &roachpb.RangeDescriptor{
		RangeID:  2,
		StartKey: roachpb.RKey("b"),
		EndKey:   roachpb.RKey("c"),
	}
	stats := engine.MVCCStats{
		LiveBytes:       1,
		KeyBytes:        2,
		ValBytes:        3,
		IntentBytes:     4,
		LiveCount:       5,
		KeyCount:        6,
		ValCount:        7,
		IntentCount:     8,
		IntentAge:       9,
		GCBytesAge:      10,
		LastUpdateNanos: 1 * 1E9,
	}

	// Create a monitor and a recorder which uses the monitor.
	closer := make(chan struct{})
	close(closer) // shut down all moving parts right away.
	monitor := NewNodeStatusMonitor(metric.NewRegistry())
	manual := hlc.NewManualClock(100)
	recorder := NewNodeStatusRecorder(monitor, hlc.NewClock(manual.UnixNano))

	// Initialization events.
	monitor.OnStartNode(&StartNodeEvent{
		Desc:      nodeDesc,
		StartedAt: 50,
	})
	monitor.OnStartStore(&storage.StartStoreEvent{
		StoreID:   roachpb.StoreID(1),
		StartedAt: 60,
	})
	monitor.OnStartStore(&storage.StartStoreEvent{
		StoreID:   roachpb.StoreID(2),
		StartedAt: 70,
	})
	monitor.OnStoreStatus(&storage.StoreStatusEvent{
		Desc: &storeDesc1,
	})
	monitor.OnStoreStatus(&storage.StoreStatusEvent{
		Desc: &storeDesc2,
	})

	// Add some data to the monitor by simulating incoming events.
	monitor.OnBeginScanRanges(&storage.BeginScanRangesEvent{
		StoreID: roachpb.StoreID(1),
	})
	monitor.OnBeginScanRanges(&storage.BeginScanRangesEvent{
		StoreID: roachpb.StoreID(2),
	})
	monitor.OnRegisterRange(&storage.RegisterRangeEvent{
		StoreID: roachpb.StoreID(1),
		Desc:    desc1,
		Stats:   stats,
		Scan:    true,
	})
	monitor.OnRegisterRange(&storage.RegisterRangeEvent{
		StoreID: roachpb.StoreID(1),
		Desc:    desc2,
		Stats:   stats,
		Scan:    true,
	})
	monitor.OnRegisterRange(&storage.RegisterRangeEvent{
		StoreID: roachpb.StoreID(2),
		Desc:    desc1,
		Stats:   stats,
		Scan:    true,
	})
	monitor.OnEndScanRanges(&storage.EndScanRangesEvent{
		StoreID: roachpb.StoreID(1),
	})
	monitor.OnEndScanRanges(&storage.EndScanRangesEvent{
		StoreID: roachpb.StoreID(2),
	})
	monitor.OnUpdateRange(&storage.UpdateRangeEvent{
		StoreID: roachpb.StoreID(1),
		Desc:    desc1,
		Delta:   stats,
	})
	// Periodically published events.
	monitor.OnReplicationStatus(&storage.ReplicationStatusEvent{
		StoreID:              roachpb.StoreID(1),
		LeaderRangeCount:     1,
		AvailableRangeCount:  2,
		ReplicatedRangeCount: 0,
	})
	monitor.OnReplicationStatus(&storage.ReplicationStatusEvent{
		StoreID:              roachpb.StoreID(2),
		LeaderRangeCount:     1,
		AvailableRangeCount:  2,
		ReplicatedRangeCount: 0,
	})
	// Node Events.
	monitor.OnCallSuccess(&CallSuccessEvent{
		NodeID: roachpb.NodeID(1),
		Method: roachpb.Get,
	})
	monitor.OnCallSuccess(&CallSuccessEvent{
		NodeID: roachpb.NodeID(1),
		Method: roachpb.Put,
	})
	monitor.OnCallError(&CallErrorEvent{
		NodeID: roachpb.NodeID(1),
		Method: roachpb.Scan,
	})

	generateNodeData := func(nodeId int, name string, time, val int64) ts.TimeSeriesData {
		return ts.TimeSeriesData{
			Name:   nodeTimeSeriesPrefix + name,
			Source: strconv.FormatInt(int64(nodeId), 10),
			Datapoints: []*ts.TimeSeriesDatapoint{
				{
					TimestampNanos: time,
					Value:          float64(val),
				},
			},
		}
	}

	generateStoreData := func(storeId int, name string, time, val int64) ts.TimeSeriesData {
		return ts.TimeSeriesData{
			Name:   storeTimeSeriesPrefix + name,
			Source: strconv.FormatInt(int64(storeId), 10),
			Datapoints: []*ts.TimeSeriesDatapoint{
				{
					TimestampNanos: time,
					Value:          float64(val),
				},
			},
		}
	}

	// Generate the expected return value of recorder.GetTimeSeriesData(). This
	// data was manually generated, but is based on a simple multiple of the
	// "stats" collection above.
	expected := []ts.TimeSeriesData{
		// Store 1 should have accumulated 3x stats from two ranges.
		generateStoreData(1, "livebytes", 100, 3),
		generateStoreData(1, "keybytes", 100, 6),
		generateStoreData(1, "valbytes", 100, 9),
		generateStoreData(1, "intentbytes", 100, 12),
		generateStoreData(1, "livecount", 100, 15),
		generateStoreData(1, "keycount", 100, 18),
		generateStoreData(1, "valcount", 100, 21),
		generateStoreData(1, "intentcount", 100, 24),
		generateStoreData(1, "intentage", 100, 27),
		generateStoreData(1, "gcbytesage", 100, 30),
		generateStoreData(1, "lastupdatenanos", 100, 1*1e9),
		generateStoreData(1, "ranges", 100, 2),
		generateStoreData(1, "ranges.leader", 100, 1),
		generateStoreData(1, "ranges.available", 100, 2),
		generateStoreData(1, "ranges.replicated", 100, 0),
		generateStoreData(1, "capacity", 100, 100),
		generateStoreData(1, "capacity.available", 100, 50),

		// Store 2 should have accumulated 1 copy of stats
		generateStoreData(2, "livebytes", 100, 1),
		generateStoreData(2, "keybytes", 100, 2),
		generateStoreData(2, "valbytes", 100, 3),
		generateStoreData(2, "intentbytes", 100, 4),
		generateStoreData(2, "livecount", 100, 5),
		generateStoreData(2, "keycount", 100, 6),
		generateStoreData(2, "valcount", 100, 7),
		generateStoreData(2, "intentcount", 100, 8),
		generateStoreData(2, "intentage", 100, 9),
		generateStoreData(2, "gcbytesage", 100, 10),
		generateStoreData(2, "lastupdatenanos", 100, 1*1e9),
		generateStoreData(2, "ranges", 100, 1),
		generateStoreData(2, "ranges.leader", 100, 1),
		generateStoreData(2, "ranges.available", 100, 2),
		generateStoreData(2, "ranges.replicated", 100, 0),
		generateStoreData(2, "capacity", 100, 200),
		generateStoreData(2, "capacity.available", 100, 75),

		// Node stats.
		generateNodeData(1, "exec.success-count", 100, 2),
		generateNodeData(1, "exec.error-count", 100, 1),
		generateNodeData(1, "exec.success-1h", 100, 0),
		generateNodeData(1, "exec.error-1h", 100, 0),
		generateNodeData(1, "exec.success-10m", 100, 0),
		generateNodeData(1, "exec.error-10m", 100, 0),
		generateNodeData(1, "exec.success-1m", 100, 0),
		generateNodeData(1, "exec.error-1m", 100, 0),
	}

	actual := recorder.GetTimeSeriesData()

	var actNumLatencyMetrics int
	expNumLatencyMetrics := len(recordHistogramQuantiles) * len(metric.DefaultTimeScales)
	for _, item := range actual {
		if ok, _ := regexp.MatchString(`cr.node.exec.latency.*`, item.Name); ok {
			actNumLatencyMetrics++
			expected = append(expected, item)
		}
	}

	if expNumLatencyMetrics != actNumLatencyMetrics {
		t.Fatalf("unexpected number of latency metrics %d, expected %d",
			actNumLatencyMetrics, expNumLatencyMetrics)
	}

	sort.Sort(byTimeAndName(actual))
	sort.Sort(byTimeAndName(expected))
	if a, e := actual, expected; !reflect.DeepEqual(a, e) {
		t.Errorf("recorder did not yield expected time series collection; diff:\n %v", pretty.Diff(e, a))
	}

	expectedNodeSummary := &NodeStatus{
		Desc:      nodeDesc,
		StartedAt: 50,
		UpdatedAt: 100,
		StoreIDs: []roachpb.StoreID{
			roachpb.StoreID(1),
			roachpb.StoreID(2),
		},
		RangeCount:           3,
		LeaderRangeCount:     2,
		AvailableRangeCount:  4,
		ReplicatedRangeCount: 0,
	}
	expectedStoreSummaries := []storage.StoreStatus{
		{
			Desc:                 storeDesc1,
			NodeID:               roachpb.NodeID(1),
			UpdatedAt:            100,
			StartedAt:            60,
			RangeCount:           2,
			LeaderRangeCount:     1,
			AvailableRangeCount:  2,
			ReplicatedRangeCount: 0,
		},
		{
			Desc:                 storeDesc2,
			NodeID:               roachpb.NodeID(1),
			StartedAt:            70,
			UpdatedAt:            100,
			RangeCount:           1,
			LeaderRangeCount:     1,
			AvailableRangeCount:  2,
			ReplicatedRangeCount: 0,
		},
	}
	// Use base stats to generate expected summary stat values.
	for i := 0; i < 3; i++ {
		expectedStoreSummaries[0].Stats.Add(&stats)
	}
	expectedStoreSummaries[1].Stats.Add(&stats)
	for _, ss := range expectedStoreSummaries {
		expectedNodeSummary.Stats.Add(&ss.Stats)
	}

	nodeSummary, storeSummaries := recorder.GetStatusSummaries()
	sort.Sort(byStoreDescID(storeSummaries))
	sort.Sort(byStoreID(nodeSummary.StoreIDs))
	if a, e := nodeSummary, expectedNodeSummary; !reflect.DeepEqual(a, e) {
		t.Errorf("recorder did not produce expected NodeSummary; diff:\n %v", pretty.Diff(e, a))
	}
	if a, e := storeSummaries, expectedStoreSummaries; !reflect.DeepEqual(a, e) {
		t.Errorf("recorder did not produce expected StoreSummaries; diff:\n %v", pretty.Diff(e, a))
	}
}
