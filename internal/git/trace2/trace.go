package trace2

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Trace denotes a node in the tree representation of Git Trace2 events. A node is not necessary
// a one-one mapping of an event.
type Trace struct {
	// Thread is the name of the thread of the corresponding event. The default thread name is
	// "main". A new thread is assigned with a new name.
	Thread string `json:"thread"`
	// Name denotes the name of the trace. The node name depends on the event types. Data-type
	// trace name is the most significant. It can be used to access the accurate data trace node
	// For example: data:index:refresh/sum_scan
	Name string `json:"name"`
	// StartTime is the starting time of the trace
	StartTime time.Time `json:"start_time"`
	// FinishTime is the finishing time of the trace
	FinishTime time.Time `json:"finish_time"`
	// Metadata is a map of metadata and data extracted from the event. A data-type trace always
	// stores its data under "data" key of this map
	Metadata map[string]string `json:"metadata"`
	// ChildID is the unique ID assigned by the parent process when it spawns a sub-process
	// The ID of root process is empty.
	ChildID string `json:"child_id,omitempty"`
	// Parent points to the parent node of the current trace. The root node's parent is nil.
	// Ignore this field when unmarshalling as it causes cyclic errors
	Parent *Trace `json:"-"`
	// Children stores the list of order-significant traces belong to the current trace
	Children []*Trace `json:"children,omitempty"`
	// Depth indicates the depth of the trace node
	Depth int `json:"depth"`
}

// IsRoot returns true if the current trace is the root of the tree
func (trace *Trace) IsRoot() bool {
	return trace.Parent == nil
}

// Walk performs in-order tree traversal. It stops at each node and trigger handler function with
// the current trace.
func (trace *Trace) Walk(ctx context.Context, handler func(context.Context, *Trace) context.Context) {
	if trace == nil {
		return
	}
	ctx = handler(ctx, trace)
	for _, child := range trace.Children {
		child.Walk(ctx, handler)
	}
}

// Inspect returns the formatted string of the tree. It mimics the format for trace2's performance
// target: https://git-scm.com/docs/api-trace2#_perf_format. It's mostly used for testing and
// debugging purpose.
func (trace *Trace) Inspect(detailed bool) string {
	var output strings.Builder
	trace.Walk(context.Background(), func(ctx context.Context, t *Trace) context.Context {
		if output.Len() != 0 {
			output.WriteString("\n")
		}
		if detailed {
			output.WriteString(fmt.Sprintf("%s | %s ",
				t.StartTime.UTC().Format(time.RFC3339Nano),
				t.FinishTime.UTC().Format(time.RFC3339Nano)))
		}
		output.WriteString(fmt.Sprintf("| %-1s | %s | %s%s",
			t.ChildID,
			t.Thread,
			strings.Repeat(".", t.Depth),
			t.Name))
		if detailed && len(t.Metadata) > 0 {
			output.WriteString(fmt.Sprintf(" %s", t.inspectMetadata()))
		}
		return ctx
	})
	return output.String()
}

func (trace *Trace) setName(hints []string) {
	var parts []string
	for _, s := range hints {
		if strings.TrimSpace(s) != "" {
			parts = append(parts, s)
		}
	}
	trace.Name = strings.Join(parts, ":")
}

// SetMetadata sets the metadata for the trace.
func (trace *Trace) SetMetadata(key, value string) {
	if trace.Metadata == nil {
		trace.Metadata = map[string]string{}
	}
	trace.Metadata[key] = value
}

func (trace *Trace) inspectMetadata() string {
	var metadata strings.Builder
	if len(trace.Metadata) > 0 {
		metadata.WriteString("(")
		keys := make([]string, 0, len(trace.Metadata))
		for key := range trace.Metadata {
			keys = append(keys, key)
		}
		// Sort metadata by key to make output deterministic
		sort.Strings(keys)
		for index, key := range keys {
			if index != 0 {
				metadata.WriteString(" ")
			}
			metadata.WriteString(fmt.Sprintf("%s=%q", key, trace.Metadata[key]))
		}
		metadata.WriteString(")")
	}
	return metadata.String()
}
