package featureflag

// CommandStatsMetrics tracks additional prometheus metrics for each shelled out command
var CommandStatsMetrics = NewFeatureFlag("command_stats_metrics", false)
