//go:build tools

// Package tools pins the OpenAPI generator so module tidy keeps it.
package tools

import (
	_ "github.com/swaggo/swag/cmd/swag"
)
