package trace2hooks

import (
	"time"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git/trace2"
)

func createExampleTrace(startTime time.Time) *trace2.Trace {
	//                         0s    1s    2s    3s    4s    5s    6s    7s
	// root                    |---->|---->|---->|---->|---->|---->|---->|
	// version                 |---->|     |     |     |     |     |     |
	// start                   |     |---->|     |     |     |     |     |
	// def_repo                |     |     |---->|     |     |     |     |
	// index:do_read_index     |     |     |     |---->|---->|---->|     |
	// cache_tree:read         |     |     |     |---->|     |     |     |
	// data:index:read/version |     |     |     |     |---->|     |     |
	// data:index:read/cache_nr|     |     |     |     |     |---->|     |
	// submodule:parallel/fetch|     |     |     |     |     |     |---->|
	return connectChildren(&trace2.Trace{
		Thread:     "main",
		Name:       "root",
		StartTime:  startTime,
		FinishTime: startTime.Add(7 * time.Second),
		Depth:      0,
		Children: []*trace2.Trace{
			{
				Thread:     "main",
				Name:       "version",
				StartTime:  startTime,
				FinishTime: startTime.Add(1 * time.Second), Metadata: map[string]string{"exe": "2.42.0"},
				Depth: 1,
			},
			{
				Thread:     "main",
				Name:       "start",
				StartTime:  startTime.Add(1 * time.Second),
				FinishTime: startTime.Add(2 * time.Second),
				Metadata:   map[string]string{"argv": "git fetch origin master"},
				Depth:      1,
			},
			{
				Thread:     "main",
				Name:       "def_repo",
				StartTime:  startTime.Add(2 * time.Second),
				FinishTime: startTime.Add(3 * time.Second),
				Metadata:   map[string]string{"worktree": "/Users/userx123/Documents/gitlab-development-kit"},
				Depth:      1,
			},
			connectChildren(&trace2.Trace{
				Thread:     "main",
				Name:       "index:do_read_index",
				StartTime:  startTime.Add(3 * time.Second),
				FinishTime: startTime.Add(6 * time.Second),
				Depth:      1,
				Children: []*trace2.Trace{
					{
						Thread:     "main",
						Name:       "cache_tree:read",
						ChildID:    "0",
						StartTime:  startTime.Add(3 * time.Second),
						FinishTime: startTime.Add(4 * time.Second),
						Depth:      2,
					},
					{
						Thread:     "main",
						ChildID:    "0",
						Name:       "data:index:read/version",
						StartTime:  startTime.Add(4 * time.Second),
						FinishTime: startTime.Add(5 * time.Second),
						Metadata:   map[string]string{"data": "2"},
						Depth:      2,
					},
					{
						Thread:     "main",
						ChildID:    "0",
						Name:       "data:index:read/cache_nr",
						StartTime:  startTime.Add(5 * time.Second),
						FinishTime: startTime.Add(6 * time.Second),
						Metadata:   map[string]string{"data": "1500"},
						Depth:      2,
					},
				},
			}),
			{
				Thread:     "main",
				Name:       "submodule:parallel/fetch",
				StartTime:  startTime.Add(6 * time.Second),
				FinishTime: startTime.Add(7 * time.Second),
				Depth:      1,
			},
		},
	})
}

func connectChildren(trace *trace2.Trace) *trace2.Trace {
	for _, t := range trace.Children {
		t.Parent = trace
	}
	return trace
}
