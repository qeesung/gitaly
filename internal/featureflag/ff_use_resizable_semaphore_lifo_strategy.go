package featureflag

// UseResizableSemaphoreLifoStrategy enables the switching of queue mechanisms
// between FIFO and LIFO queues controlling how requests are queued up.
var UseResizableSemaphoreLifoStrategy = NewFeatureFlag(
	"use_resizable_semaphore_lifo_strategy",
	"v16.10",
	"https://gitlab.com/gitlab-org/gitaly/-/issues/5396",
	false,
)
