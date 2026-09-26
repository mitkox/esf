package factory

import _ "embed"

// exampleConfig is the single source for factory init and the checked-in
// factory.example.toml symlink.
//
//go:embed example.toml
var exampleConfig string

func ExampleConfig() string { return exampleConfig }
