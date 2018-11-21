package correlation

// The configuration for InjectCorrelationID
type inboundHandlerConfig struct {
}

// InboundHandlerOption will configure a correlation handler
// currently there are no options, but this gives us the option
// to extend the interface in a backwards compatible way
type InboundHandlerOption func(*inboundHandlerConfig)

func applyInboundHandlerOptions(opts []InboundHandlerOption) inboundHandlerConfig {
	config := inboundHandlerConfig{}
	for _, v := range opts {
		v(&config)
	}

	return config
}
