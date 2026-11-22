package config

// Stage accessors let config_test exercise the production parse and
// instance-validation stages without a package loader or layout fixtures.
var (
	ParseYAMLStage             = parseYAMLStage
	ValidateCoreInstancesStage = validateCoreInstancesStage
)
