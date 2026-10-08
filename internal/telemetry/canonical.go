package telemetry

import (
	"encoding/json"
	"sort"
)

// Canonicalize removes capture-request ordering and batching differences from
// the policy view. Resource boundaries remain intact for resource assertions.
// This is deliberately opt-in so existing fixture report indexes stay stable.
func Canonicalize(set *Set) {
	if set == nil {
		return
	}
	grouped := make(map[string]*Metric)
	for _, metric := range set.Metrics {
		key := jsonKey(struct {
			Name     string
			Resource map[string]string
		}{metric.Name, metric.ResourceAttributes})
		if existing := grouped[key]; existing != nil {
			existing.Datapoints = append(existing.Datapoints, metric.Datapoints...)
		} else {
			copy := metric
			copy.Datapoints = append([]Datapoint(nil), metric.Datapoints...)
			grouped[key] = &copy
		}
	}
	set.Metrics = nil
	for _, metric := range grouped {
		sortByJSON(metric.Datapoints)
		set.Metrics = append(set.Metrics, *metric)
	}
	sortByJSON(set.Logs)
	sortByJSON(set.Spans)
	sortByJSON(set.Metrics)
}

func jsonKey(value any) string {
	data, _ := json.Marshal(value) // Only string/map/slice policy model types.
	return string(data)
}

func sortByJSON[T any](values []T) {
	// Encode once rather than for every comparison on large fixtures.
	type entry struct {
		key   string
		value T
	}
	entries := make([]entry, len(values))
	for i, value := range values {
		entries[i] = entry{jsonKey(value), value}
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].key < entries[j].key })
	for i := range entries {
		values[i] = entries[i].value
	}
}
