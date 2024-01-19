package trace2

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
)

func TestParser_Parse(t *testing.T) {
	t.Parallel()

	cases := []struct {
		desc          string
		events        string
		expectedErr   string
		expectedTrace string
	}{
		{
			desc:   "empty events",
			events: "",
		},
		{
			desc:        "invalid json format",
			events:      "hello",
			expectedErr: "reading event: decoding event: invalid character 'h' looking for beginning of value",
		},
		{
			desc: "incomplete events",
			events: `
{"event":"version","thread":"main","time":"2023-02-22T07:24:36.291735Z","file":"common-main.c","line":50,"evt":"3","exe":"2.39.1"}
{"event":"start","thread":"main"}
{""
`,
			expectedErr: "reading event: decoding event: unexpected EOF",
		},
		{
			desc:        "mismatched event data type",
			events:      `{"event":"start","thread":"main","t_abs": "1234"}`,
			expectedErr: "reading event: decoding event: json: cannot unmarshal string into Go struct field jsonEvent.t_abs of type float64",
		},
		{
			desc:        "invalid time",
			events:      `{"event":"start","thread":"main","time": "1234"}`,
			expectedErr: `processing event: parsing event time: parsing time "1234" as "2006-01-02T15:04:05.000000Z": cannot parse "" as "-"`,
		},
		{
			desc: "mismatched data value format",
			events: `
{"event":"start","thread":"main","time":"2023-02-22T07:24:36.291735Z"}
{"event":"data","thread":"main","key": "hello", "value": 123}
`,
			expectedErr: `processing event: mismatched data value: json: cannot unmarshal number into Go value of type string`,
		},
		{
			desc: "unexpected region_leave event",
			events: `
{"event":"version","sid":"1234/20230222T120504.839855Z-Ha0f0bee5-P0000ccc6","thread":"main","time":"2023-02-22T12:05:04.840009Z","file":"common-main.c","line":50,"evt":"3","exe":"2.39.1"}
{"event":"start","sid":"1234/20230222T120504.839855Z-Ha0f0bee5-P0000ccc6","thread":"main","file":"common-main.c","line":51,"t_abs":0.002338,"argv":["git","pack-objects","toon","--compression=0"]}
{"event":"def_repo","sid":"1234/20230222T120504.839855Z-Ha0f0bee5-P0000ccc6","thread":"main","file":"repository.c","line":136,"repo":1,"worktree":"/gitaly"}
{"event":"cmd_name","sid":"1234/20230222T120504.839855Z-Ha0f0bee5-P0000ccc6","thread":"main","file":"git.c","line":461,"name":"pack-objects","hierarchy":"pack-objects"}
{"event":"region_enter","sid":"1234/20230222T120504.839855Z-Ha0f0bee5-P0000ccc6","thread":"main","file":"builtin/pack-objects.c","line":4460,"repo":1,"nesting":1,"category":"pack-objects","label":"enumerate-objects"}
{"event":"region_enter","sid":"1234/20230222T120504.839855Z-Ha0f0bee5-P0000ccc6","thread":"main","file":"progress.c","line":268,"repo":1,"nesting":2,"category":"progress","label":"Enumerating objects"}
{"event":"region_leave","sid":"1234/20230222T120504.839855Z-Ha0f0bee5-P0000ccc6","thread":"main","file":"progress.c","line":346,"repo":1,"t_rel":0.001083,"nesting":2,"category":"progress","label":"Enumerating objects"}
{"event":"region_leave","sid":"1234/20230222T120504.839855Z-Ha0f0bee5-P0000ccc6","thread":"main","file":"builtin/pack-objects.c","line":4490,"repo":1,"t_rel":0.001435,"nesting":1,"category":"pack-objects","label":"enumerate-objects"}
{"event":"region_leave","sid":"1234/20230222T120504.839855Z-Ha0f0bee5-P0000ccc6","thread":"main","file":"builtin/pack-objects.c","line":4490,"repo":1,"t_rel":0.001435,"nesting":1,"category":"pack-objects","label":"enumerate-objects"}
{"event":"region_leave","sid":"1234/20230222T120504.839855Z-Ha0f0bee5-P0000ccc6","thread":"main","file":"builtin/pack-objects.c","line":4490,"repo":1,"t_rel":0.001435,"nesting":1,"category":"pack-objects","label":"enumerate-objects"}
{"event":"exit","sid":"1234/20230222T120504.839855Z-Ha0f0bee5-P0000ccc6","thread":"main","file":"git.c","line":721,"t_abs":0.008489,"code":0}
{"event":"atexit","sid":"1234/20230222T120504.839855Z-Ha0f0bee5-P0000ccc6","thread":"main","file":"trace2/tr2_tgt_event.c","line":204,"t_abs":0.008495,"code":0}
`,
			expectedErr: `processing event: unmatched leaving event`,
		},
		{
			desc: "unexpected child_exit event",
			events: `
{"event":"version","sid":"1234/20230222T072436.291562Z-Ha0f0bee5-P00007515","thread":"main","time":"2023-02-22T07:24:36.291735Z","file":"common-main.c","line":50,"evt":"3","exe":"2.39.1"}
{"event":"start","sid":"1234/20230222T072436.291562Z-Ha0f0bee5-P00007515","thread":"main","file":"common-main.c","line":51,"t_abs":0.002197,"argv":["git","fetch","origin","master"]}
{"event":"def_repo","sid":"1234/20230222T072436.291562Z-Ha0f0bee5-P00007515","thread":"main","file":"repository.c","line":136,"repo":1,"worktree":"/gitaly"}
{"event":"cmd_name","sid":"1234/20230222T072436.291562Z-Ha0f0bee5-P00007515","thread":"main","file":"git.c","line":461,"name":"fetch","hierarchy":"fetch"}
{"event":"region_enter","sid":"1234/20230222T072436.291562Z-Ha0f0bee5-P00007515","thread":"main","file":"read-cache.c","line":2478,"repo":1,"nesting":1,"category":"index","label":"do_read_index","msg":".git/index"}
{"event":"region_enter","sid":"1234/20230222T072436.291562Z-Ha0f0bee5-P00007515","thread":"main","file":"cache-tree.c","line":628,"repo":1,"nesting":2,"category":"cache_tree","label":"read"}
{"event":"region_leave","sid":"1234/20230222T072436.291562Z-Ha0f0bee5-P00007515","thread":"main","file":"cache-tree.c","line":630,"repo":1,"t_rel":0.000043,"nesting":2,"category":"cache_tree","label":"read"}
{"event":"child_start","sid":"1234/20230222T072436.291562Z-Ha0f0bee5-P00007515","thread":"main","file":"run-command.c","line":722,"child_id":0,"child_class":"transport/ssh","use_shell":false,"argv":["ssh","-o","SendEnv=GIT_PROTOCOL","git@gitlab.com","git-upload-pack 'gitlab-org/gitaly.git'"]}
{"event":"data","sid":"1234/20230222T072436.291562Z-Ha0f0bee5-P00007515","thread":"main","file":"connect.c","line":167,"t_abs":3.023040,"t_rel":3.018289,"nesting":2,"category":"transfer","key":"negotiated-version","value":"2"}
{"event":"region_leave","sid":"1234/20230222T072436.291562Z-Ha0f0bee5-P00007515","thread":"main","file":"builtin/fetch.c","line":1649,"repo":1,"t_rel":3.834328,"nesting":1,"category":"fetch","label":"remote_refs"}
{"event":"child_start","sid":"1234/20230222T072436.291562Z-Ha0f0bee5-P00007515","thread":"main","file":"run-command.c","line":722,"child_id":1,"child_class":"?","use_shell":false,"argv":["git","rev-list","--objects","--stdin","--not","--all","--quiet","--alternate-refs"]}
{"event":"version","sid":"1234/20230222T072436.291562Z-Ha0f0bee5-P00007515/20230222T072440.148843Z-Ha0f0bee5-P0000751a","thread":"main","time":"2023-02-22T07:24:40.148998Z","file":"common-main.c","line":50,"evt":"3","exe":"2.39.1"}
{"event":"start","sid":"1234/20230222T072436.291562Z-Ha0f0bee5-P00007515/20230222T072440.148843Z-Ha0f0bee5-P0000751a","thread":"main","file":"common-main.c","line":51,"t_abs":0.003901,"argv":["git","rev-list","--objects","--stdin","--not","--all","--quiet","--alternate-refs"]}
{"event":"def_repo","sid":"1234/20230222T072436.291562Z-Ha0f0bee5-P00007515/20230222T072440.148843Z-Ha0f0bee5-P0000751a","thread":"main","file":"repository.c","line":136,"repo":1,"worktree":"/gitaly"}
{"event":"cmd_name","sid":"1234/20230222T072436.291562Z-Ha0f0bee5-P00007515/20230222T072440.148843Z-Ha0f0bee5-P0000751a","thread":"main","file":"git.c","line":461,"name":"rev-list","hierarchy":"fetch/rev-list"}
{"event":"exit","sid":"1234/20230222T072436.291562Z-Ha0f0bee5-P00007515/20230222T072440.148843Z-Ha0f0bee5-P0000751a","thread":"main","file":"git.c","line":721,"t_abs":0.032566,"code":0}
{"event":"atexit","sid":"1234/20230222T072436.291562Z-Ha0f0bee5-P00007515/20230222T072440.148843Z-Ha0f0bee5-P0000751a","thread":"main","file":"trace2/tr2_tgt_event.c","line":204,"t_abs":0.032593,"code":0}
{"event":"child_exit","sid":"1234/20230222T072436.291562Z-Ha0f0bee5-P00007515","thread":"main","file":"run-command.c","line":979,"child_id":1,"pid":29978,"code":0,"t_rel":0.036620}
{"event":"child_exit","sid":"1234/20230222T072436.291562Z-Ha0f0bee5-P00007515","thread":"main","file":"run-command.c","line":979,"child_id":0,"pid":29974,"code":0,"t_rel":4.245077}
{"event":"region_enter","sid":"1234/20230222T072436.291562Z-Ha0f0bee5-P00007515","thread":"main","file":"builtin/fetch.c","line":1353,"repo":1,"nesting":1,"category":"fetch","label":"consume_refs"}
{"event":"region_leave","sid":"1234/20230222T072436.291562Z-Ha0f0bee5-P00007515","thread":"main","file":"builtin/fetch.c","line":1357,"repo":1,"t_rel":0.000733,"nesting":1,"category":"fetch","label":"consume_refs"}
{"event":"child_exit","sid":"1234/20230222T072436.291562Z-Ha0f0bee5-P00007515","thread":"main","file":"run-command.c","line":979,"child_id":0,"pid":29974,"code":0,"t_rel":4.245077}
{"event":"exit","sid":"1234/20230222T072436.291562Z-Ha0f0bee5-P00007515","thread":"main","file":"git.c","line":721,"t_abs":4.262658,"code":0}
{"event":"atexit","sid":"1234/20230222T072436.291562Z-Ha0f0bee5-P00007515","thread":"main","file":"trace2/tr2_tgt_event.c","line":204,"t_abs":4.262672,"code":0}
`,
			expectedErr: `processing event: unmatched leaving event`,
		},
		{
			desc: "incomplete events without enough leaving events",
			events: `
{"event":"version","sid":"1234/20230222T120504.839855Z-Ha0f0bee5-P0000ccc6","thread":"main","time":"2023-02-22T12:05:04.840009Z","file":"common-main.c","line":50,"evt":"3","exe":"2.39.1"}
{"event":"start","sid":"1234/20230222T120504.839855Z-Ha0f0bee5-P0000ccc6","thread":"main","file":"common-main.c","line":51,"t_abs":0.002338,"argv":["git","pack-objects","toon","--compression=0"]}
{"event":"def_repo","sid":"1234/20230222T120504.839855Z-Ha0f0bee5-P0000ccc6","thread":"main","file":"repository.c","line":136,"repo":1,"worktree":"/gitaly"}
{"event":"cmd_name","sid":"1234/20230222T120504.839855Z-Ha0f0bee5-P0000ccc6","thread":"main","file":"git.c","line":461,"name":"pack-objects","hierarchy":"pack-objects"}
{"event":"region_enter","sid":"1234/20230222T120504.839855Z-Ha0f0bee5-P0000ccc6","thread":"main","file":"builtin/pack-objects.c","line":4460,"repo":1,"nesting":1,"category":"pack-objects","label":"enumerate-objects"}
{"event":"region_enter","sid":"1234/20230222T120504.839855Z-Ha0f0bee5-P0000ccc6","thread":"main","file":"progress.c","line":268,"repo":1,"nesting":2,"category":"progress","label":"Enumerating objects"}
{"event":"region_leave","sid":"1234/20230222T120504.839855Z-Ha0f0bee5-P0000ccc6","thread":"main","file":"progress.c","line":346,"repo":1,"t_rel":0.001083,"nesting":2,"category":"progress","label":"Enumerating objects"}
`,
			expectedTrace: `
2023-02-22T12:05:04.840009Z | 0001-01-01T00:00:00Z |   | main | root
2023-02-22T12:05:04.840009Z | 2023-02-22T12:05:04.840009Z |   | main | .version (evt="3" exe="2.39.1")
2023-02-22T12:05:04.842347Z | 2023-02-22T12:05:04.842347Z |   | main | .start (argv="git pack-objects toon --compression=0")
2023-02-22T12:05:04.842347Z | 2023-02-22T12:05:04.842347Z |   | main | .def_repo (worktree="/gitaly")
2023-02-22T12:05:04.842347Z | 2023-02-22T12:05:04.842347Z |   | main | .pack-objects:enumerate-objects
2023-02-22T12:05:04.842347Z | 2023-02-22T12:05:04.84343Z |   | main | ..progress:Enumerating objects
`,
		},
		{
			desc: "initial time is missing",
			events: `
{"event":"version","sid":"1234/20230222T120504.839855Z-Ha0f0bee5-P0000ccc6","thread":"main","file":"common-main.c","line":50,"evt":"3","exe":"2.39.1"}
{"event":"start","sid":"1234/20230222T120504.839855Z-Ha0f0bee5-P0000ccc6","thread":"main","file":"common-main.c","line":51,"t_abs":0.002338,"argv":["git","pack-objects","toon","--compression=0"]}
{"event":"def_repo","sid":"1234/20230222T120504.839855Z-Ha0f0bee5-P0000ccc6","thread":"main","file":"repository.c","line":136,"repo":1,"worktree":"/gitaly"}
{"event":"cmd_name","sid":"1234/20230222T120504.839855Z-Ha0f0bee5-P0000ccc6","thread":"main","file":"git.c","line":461,"name":"pack-objects","hierarchy":"pack-objects"}
{"event":"region_enter","sid":"1234/20230222T120504.839855Z-Ha0f0bee5-P0000ccc6","thread":"main","file":"builtin/pack-objects.c","line":4460,"repo":1,"nesting":1,"category":"pack-objects","label":"enumerate-objects"}
{"event":"region_enter","sid":"1234/20230222T120504.839855Z-Ha0f0bee5-P0000ccc6","thread":"main","file":"progress.c","line":268,"repo":1,"nesting":2,"category":"progress","label":"Enumerating objects"}
{"event":"region_leave","sid":"1234/20230222T120504.839855Z-Ha0f0bee5-P0000ccc6","thread":"main","file":"progress.c","line":346,"repo":1,"t_rel":0.001083,"nesting":2,"category":"progress","label":"Enumerating objects"}
{"event":"region_leave","sid":"1234/20230222T120504.839855Z-Ha0f0bee5-P0000ccc6","thread":"main","file":"builtin/pack-objects.c","line":4490,"repo":1,"t_rel":0.001435,"nesting":1,"category":"pack-objects","label":"enumerate-objects"}
{"event":"exit","sid":"1234/20230222T120504.839855Z-Ha0f0bee5-P0000ccc6","thread":"main","file":"git.c","line":721,"t_abs":0.008489,"code":0}
{"event":"atexit","sid":"1234/20230222T120504.839855Z-Ha0f0bee5-P0000ccc6","thread":"main","file":"trace2/tr2_tgt_event.c","line":204,"t_abs":0.008495,"code":0}
`,
			expectedErr: `processing event: parsing event time: initial time is missing`,
		},
		{
			desc: "initial time is missing",
			events: `
{"event":"version","sid":"1234/20230222T120504.839855Z-Ha0f0bee5-P0000ccc6","thread":"main","file":"common-main.c","line":50,"evt":"3","exe":"2.39.1", "t_abs":0.002338}
{"event":"start","sid":"1234/20230222T120504.839855Z-Ha0f0bee5-P0000ccc6","thread":"main","file":"common-main.c","line":51,"t_abs":0.002338,"argv":["git","pack-objects","toon","--compression=0"]}
`,
			expectedErr: `processing event: parsing event time: initial time is missing`,
		},
		{
			desc:   "sampled git status events",
			events: string(testhelper.MustReadFile(t, "testdata/git-status.event")),
			expectedTrace: `
2023-02-21T08:10:10.668546Z | 2023-02-21T08:10:10.687877Z |   | main | root (code="0")
2023-02-21T08:10:10.668546Z | 2023-02-21T08:10:10.668546Z |   | main | .version (evt="3" exe="2.39.1")
2023-02-21T08:10:10.754608Z | 2023-02-21T08:10:10.754608Z |   | main | .start (argv="git status")
2023-02-21T08:10:10.754608Z | 2023-02-21T08:10:10.754608Z |   | main | .def_repo (worktree="/Users/userx/www/gitlab-development-kit/gitaly")
2023-02-21T08:10:10.754608Z | 2023-02-21T08:10:10.755102Z |   | main | .index:do_read_index (msg=".git/index")
2023-02-21T08:10:10.754608Z | 2023-02-21T08:10:10.754732Z |   | main | ..cache_tree:read
2023-02-21T08:10:10.758297Z | 2023-02-21T08:10:10.758297Z |   | main | ..data:index:read/version (data="2")
2023-02-21T08:10:10.758318Z | 2023-02-21T08:10:10.758318Z |   | main | ..data:index:read/cache_nr (data="1585")
2023-02-21T08:10:10.755102Z | 2023-02-21T08:10:10.759927Z |   | main | .progress:Refresh index
2023-02-21T08:10:10.755102Z | 2023-02-21T08:10:10.759845Z |   | main | ..index:preload
2023-02-21T08:10:10.759845Z | 2023-02-21T08:10:10.759862Z |   | main | ..index:refresh
2023-02-21T08:10:10.763258Z | 2023-02-21T08:10:10.763258Z |   | main | ..data:progress:total_objects (data="1585")
2023-02-21T08:10:10.759927Z | 2023-02-21T08:10:10.759999Z |   | main | .status:worktrees
2023-02-21T08:10:10.759927Z | 2023-02-21T08:10:10.759932Z |   | main | ..diff:setup
2023-02-21T08:10:10.759932Z | 2023-02-21T08:10:10.759936Z |   | main | ..diff:write back to queue
2023-02-21T08:10:10.759999Z | 2023-02-21T08:10:10.761522Z |   | main | .status:index
2023-02-21T08:10:10.759999Z | 2023-02-21T08:10:10.760078Z |   | main | ..unpack_trees:unpack_trees
2023-02-21T08:10:10.760078Z | 2023-02-21T08:10:10.760081Z |   | main | ..diff:setup
2023-02-21T08:10:10.760081Z | 2023-02-21T08:10:10.760084Z |   | main | ..diff:write back to queue
2023-02-21T08:10:10.761522Z | 2023-02-21T08:10:10.769788Z |   | main | .status:untracked
2023-02-21T08:10:10.761522Z | 2023-02-21T08:10:10.769753Z |   | main | ..dir:read_directory
2023-02-21T08:10:10.773512Z | 2023-02-21T08:10:10.773512Z |   | main | .data:status:count/changed (data="0")
2023-02-21T08:10:10.773518Z | 2023-02-21T08:10:10.773518Z |   | main | .data:status:count/untracked (data="1")
2023-02-21T08:10:10.773522Z | 2023-02-21T08:10:10.773522Z |   | main | .data:status:count/ignored (data="0")
2023-02-21T08:10:10.773522Z | 2023-02-21T08:10:10.773727Z |   | main | .status:print
2023-02-21T08:10:10.77375Z | 2023-02-21T08:10:10.77375Z |   | main | .data_json:traverse_trees:statistics (data="{\"traverse_trees_count\":1,\"traverse_trees_max_depth\":1}")
`,
		},
		{
			desc:   "sampled git fetch events",
			events: string(testhelper.MustReadFile(t, "testdata/git-fetch.event")),
			expectedTrace: `
2023-02-22T07:24:36.291735Z | 2023-02-22T07:24:40.554407Z |   | main | root (code="0")
2023-02-22T07:24:36.291735Z | 2023-02-22T07:24:36.291735Z |   | main | .version (evt="3" exe="2.39.1")
2023-02-22T07:24:36.293932Z | 2023-02-22T07:24:36.293932Z |   | main | .start (argv="git fetch origin master")
2023-02-22T07:24:36.293932Z | 2023-02-22T07:24:36.293932Z |   | main | .def_repo (worktree="/gitaly")
2023-02-22T07:24:36.293932Z | 2023-02-22T07:24:36.294119Z |   | main | .index:do_read_index (msg=".git/index")
2023-02-22T07:24:36.293932Z | 2023-02-22T07:24:36.293975Z |   | main | ..cache_tree:read
2023-02-22T07:24:36.294718Z | 2023-02-22T07:24:36.294718Z |   | main | ..data:index:read/version (data="2")
2023-02-22T07:24:36.294725Z | 2023-02-22T07:24:36.294725Z |   | main | ..data:index:read/cache_nr (data="1589")
2023-02-22T07:24:36.294119Z | 2023-02-22T07:24:40.539196Z |   | main | .fetch:remote_refs (code="0")
2023-02-22T07:24:36.294119Z | 2023-02-22T07:24:40.128447Z | 0 | main | ..child_start (argv="ssh -o SendEnv=GIT_PROTOCOL git@gitlab.com git-upload-pack 'gitlab-org/gitaly.git'")
2023-02-22T07:24:39.314775Z | 2023-02-22T07:24:39.314775Z | 0 | main | ...data:transfer:negotiated-version (data="2")
2023-02-22T07:24:40.128447Z | 2023-02-22T07:24:40.165067Z | 1 | main | ..child_start (argv="git rev-list --objects --stdin --not --all --quiet --alternate-refs" code="0")
2023-02-22T07:24:40.148998Z | 2023-02-22T07:24:40.148998Z | 1 | main | ...version (evt="3" exe="2.39.1")
2023-02-22T07:24:36.295636Z | 2023-02-22T07:24:36.295636Z | 1 | main | ...start (argv="git rev-list --objects --stdin --not --all --quiet --alternate-refs")
2023-02-22T07:24:36.295636Z | 2023-02-22T07:24:36.295636Z | 1 | main | ...def_repo (worktree="/gitaly")
2023-02-22T07:24:40.165067Z | 2023-02-22T07:24:40.1658Z |   | main | ..fetch:consume_refs
2023-02-22T07:24:40.539196Z | 2023-02-22T07:24:40.539281Z |   | main | .submodule:parallel/fetch (msg="max:1")
2023-02-22T07:24:40.539281Z | 2023-02-22T07:24:40.550543Z | 2 | main | .child_start (argv="git maintenance run --auto --no-quiet" code="0")
2023-02-22T07:24:40.54863Z | 2023-02-22T07:24:40.54863Z | 2 | main | ..version (evt="3" exe="2.39.1")
2023-02-22T07:24:36.294627Z | 2023-02-22T07:24:36.294627Z | 2 | main | ..start (argv="git maintenance run --auto --no-quiet")
2023-02-22T07:24:36.294627Z | 2023-02-22T07:24:36.294627Z | 2 | main | ..def_repo (worktree="/gitaly")
`,
		},
		{
			desc:   "sampled git commit events",
			events: string(testhelper.MustReadFile(t, "testdata/git-commit.event")),
			expectedTrace: `
2023-02-22T11:26:37.174893Z | 2023-02-22T11:26:38.677971Z |   | main | root (code="0")
2023-02-22T11:26:37.174893Z | 2023-02-22T11:26:37.174893Z |   | main | .version (evt="3" exe="2.39.1")
2023-02-22T11:26:37.180753Z | 2023-02-22T11:26:37.180753Z |   | main | .start (argv="git commit --amend")
2023-02-22T11:26:37.180753Z | 2023-02-22T11:26:37.180753Z |   | main | .def_repo (worktree="/gitaly")
2023-02-22T11:26:37.180753Z | 2023-02-22T11:26:37.181072Z |   | main | .index:do_read_index (msg=".git/index")
2023-02-22T11:26:37.180753Z | 2023-02-22T11:26:37.180832Z |   | main | ..cache_tree:read
2023-02-22T11:26:37.185091Z | 2023-02-22T11:26:37.185091Z |   | main | ..data:index:read/version (data="2")
2023-02-22T11:26:37.185105Z | 2023-02-22T11:26:37.185105Z |   | main | ..data:index:read/cache_nr (data="1590")
2023-02-22T11:26:37.181072Z | 2023-02-22T11:26:37.18727Z |   | main | .index:preload
2023-02-22T11:26:37.191313Z | 2023-02-22T11:26:37.191313Z |   | main | ..data:index:preload/sum_lstat (data="1590")
2023-02-22T11:26:37.18727Z | 2023-02-22T11:26:37.187432Z |   | main | .index:preload
2023-02-22T11:26:37.191812Z | 2023-02-22T11:26:37.191812Z |   | main | ..data:index:preload/sum_lstat (data="0")
2023-02-22T11:26:37.187432Z | 2023-02-22T11:26:37.187492Z |   | main | .index:refresh
2023-02-22T11:26:37.191874Z | 2023-02-22T11:26:37.191874Z |   | main | ..data:index:refresh/sum_lstat (data="0")
2023-02-22T11:26:37.191906Z | 2023-02-22T11:26:37.191906Z |   | main | ..data:index:refresh/sum_scan (data="0")
2023-02-22T11:26:37.187492Z | 2023-02-22T11:26:37.194463Z |   | main | .cache_tree:update
2023-02-22T11:26:37.194463Z | 2023-02-22T11:26:37.194919Z |   | main | .index:do_write_index (msg="/gitaly/.git/index.lock")
2023-02-22T11:26:37.194463Z | 2023-02-22T11:26:37.194533Z |   | main | ..cache_tree:write
2023-02-22T11:26:37.199369Z | 2023-02-22T11:26:37.199369Z |   | main | ..data:index:write/version (data="2")
2023-02-22T11:26:37.199382Z | 2023-02-22T11:26:37.199382Z |   | main | ..data:index:write/cache_nr (data="1590")
2023-02-22T11:26:37.194919Z | 2023-02-22T11:26:37.194991Z |   | main | .status:worktrees
2023-02-22T11:26:37.194919Z | 2023-02-22T11:26:37.194927Z |   | main | ..diff:setup
2023-02-22T11:26:37.194927Z | 2023-02-22T11:26:37.194933Z |   | main | ..diff:write back to queue
2023-02-22T11:26:37.194991Z | 2023-02-22T11:26:37.195415Z |   | main | .status:index
2023-02-22T11:26:37.194991Z | 2023-02-22T11:26:37.195147Z |   | main | ..unpack_trees:unpack_trees
2023-02-22T11:26:37.195147Z | 2023-02-22T11:26:37.195154Z |   | main | ..diff:setup
2023-02-22T11:26:37.195154Z | 2023-02-22T11:26:37.195162Z |   | main | ..diff:write back to queue
2023-02-22T11:26:37.195415Z | 2023-02-22T11:26:37.208155Z |   | main | .status:untracked
2023-02-22T11:26:37.195415Z | 2023-02-22T11:26:37.208106Z |   | main | ..dir:read_directory
2023-02-22T11:26:37.214048Z | 2023-02-22T11:26:37.214048Z |   | main | .data:status:count/changed (data="5")
2023-02-22T11:26:37.214055Z | 2023-02-22T11:26:37.214055Z |   | main | .data:status:count/untracked (data="0")
2023-02-22T11:26:37.21406Z | 2023-02-22T11:26:37.21406Z |   | main | .data:status:count/ignored (data="0")
2023-02-22T11:26:37.21406Z | 2023-02-22T11:26:37.214271Z |   | main | .status:print
2023-02-22T11:26:37.214271Z | 2023-02-22T11:26:37.214297Z |   | main | .cache_tree:update
2023-02-22T11:26:37.214297Z | 2023-02-22T11:26:38.663953Z | 0 | main | .child_start (argv="nvim /gitaly/.git/COMMIT_EDITMSG" code="0")
2023-02-22T11:26:37.353241Z | 2023-02-22T11:26:37.353241Z | 0 | main | ..version (evt="3" exe="2.39.1")
2023-02-22T11:26:37.175671Z | 2023-02-22T11:26:37.175671Z | 0 | main | ..start (argv="git diff --no-color --no-ext-diff -U0 -- COMMIT_EDITMSG")
2023-02-22T11:26:37.175671Z | 2023-02-22T11:26:37.175671Z | 0 | main | ..error (msg="this operation must be run in a work tree")
2023-02-22T11:26:38.624567Z | 2023-02-22T11:26:38.624567Z | 0 | main | ..version (evt="3" exe="2.39.1")
2023-02-22T11:26:37.175517Z | 2023-02-22T11:26:37.175517Z | 0 | main | ..start (argv="git branch --no-color --show-current")
2023-02-22T11:26:37.175517Z | 2023-02-22T11:26:37.175517Z | 0 | main | ..def_repo (worktree="/gitaly")
2023-02-22T11:26:38.663953Z | 2023-02-22T11:26:38.672888Z | 1 | main | .child_start (argv="git maintenance run --auto --no-quiet" code="0")
2023-02-22T11:26:38.667832Z | 2023-02-22T11:26:38.667832Z | 1 | main | ..version (evt="3" exe="2.39.1")
2023-02-22T11:26:37.178802Z | 2023-02-22T11:26:37.178802Z | 1 | main | ..start (argv="git maintenance run --auto --no-quiet")
2023-02-22T11:26:37.178802Z | 2023-02-22T11:26:37.178802Z | 1 | main | ..def_repo (worktree="/gitaly")
2023-02-22T11:26:38.672888Z | 2023-02-22T11:26:38.672902Z |   | main | .diff:setup
2023-02-22T11:26:38.672902Z | 2023-02-22T11:26:38.672907Z |   | main | .diff:write back to queue
2023-02-22T11:26:38.677965Z | 2023-02-22T11:26:38.677965Z |   | main | .data_json:traverse_trees:statistics (data="{\"traverse_trees_count\":2,\"traverse_trees_max_depth\":2}")
`,
		},
		{
			desc:   "sampled git pack objects events",
			events: string(testhelper.MustReadFile(t, "testdata/git-pack-objects.event")),
			expectedTrace: `
2023-02-22T12:05:04.840009Z | 2023-02-22T12:05:04.848504Z |   | main | root (code="0")
2023-02-22T12:05:04.840009Z | 2023-02-22T12:05:04.840009Z |   | main | .version (evt="3" exe="2.39.1")
2023-02-22T12:05:04.842347Z | 2023-02-22T12:05:04.842347Z |   | main | .start (argv="git pack-objects toon --compression=0")
2023-02-22T12:05:04.842347Z | 2023-02-22T12:05:04.842347Z |   | main | .def_repo (worktree="/gitaly")
2023-02-22T12:05:04.842347Z | 2023-02-22T12:05:04.843782Z |   | main | .pack-objects:enumerate-objects
2023-02-22T12:05:04.842347Z | 2023-02-22T12:05:04.84343Z |   | main | ..progress:Enumerating objects
2023-02-22T12:05:04.843782Z | 2023-02-22T12:05:04.843872Z |   | main | .pack-objects:prepare-pack
2023-02-22T12:05:04.843782Z | 2023-02-22T12:05:04.843857Z |   | main | ..progress:Counting objects
2023-02-22T12:05:04.843872Z | 2023-02-22T12:05:04.847874999Z |   | main | .pack-objects:write-pack-file
2023-02-22T12:05:04.843872Z | 2023-02-22T12:05:04.847844Z |   | main | ..progress:Writing objects
2023-02-22T12:05:04.84846Z | 2023-02-22T12:05:04.84846Z |   | main | ..data:pack-objects:write_pack_file/wrote (data="1")
2023-02-22T12:05:04.848491Z | 2023-02-22T12:05:04.848491Z |   | main | .data:fsync:fsync/writeout-only (data="2")
`,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			trace, err := Parse(testhelper.Context(t), strings.NewReader(tc.events))

			if tc.expectedErr != "" {
				// JSON doesn't export error creation. It's not feasible to compare
				// error directly. We can only compare by the stringified error
				require.Error(t, err)
				require.Equal(t, tc.expectedErr, err.Error())
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, strings.TrimSpace(tc.expectedTrace), trace.Inspect(true))
		})
	}
}
