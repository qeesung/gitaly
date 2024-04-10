package main

import (
	"fmt"

	"github.com/mitchellh/mapstructure"
	"golang.org/x/tools/go/analysis"
)

// Cfg contains the configuration options for the custom linters.
type Cfg struct {
	QuoteInterpolation *quoteInterpolationAnalyzerSettings `mapstructure:"string_interpolation_quote"`
	ErrorWrap          *errorWrapAnalyzerSettings          `mapstructure:"error_wrap"`
	UnavailableCode    *unavailableCodeAnalyzerSettings    `mapstructure:"unavailable_code"`
	TesthelperRun      *testhelperRunAnalyzerSettings      `mapstructure:"testhelper_run"`
}

// New initializes Gitaly's custom linters.
func New(conf any) ([]*analysis.Analyzer, error) {
	var cfg Cfg
	if err := mapstructure.Decode(conf, &cfg); err != nil {
		return nil, fmt.Errorf("parsing gitaly-linters config: %w", err)
	}

	return []*analysis.Analyzer{
		newQuoteInterpolationAnalyzer(&quoteInterpolationAnalyzerSettings{
			IncludedFunctions: cfg.QuoteInterpolation.IncludedFunctions,
		}),
		newErrorWrapAnalyzer(&errorWrapAnalyzerSettings{
			IncludedFunctions: cfg.ErrorWrap.IncludedFunctions,
		}),
		newUnavailableCodeAnalyzer(&unavailableCodeAnalyzerSettings{
			IncludedFunctions: cfg.UnavailableCode.IncludedFunctions,
		}),
		newTesthelperRunAnalyzer(&testhelperRunAnalyzerSettings{
			IncludedFunctions: cfg.TesthelperRun.IncludedFunctions,
		}),
		newTestParamsOrder(),
	}, nil
}
