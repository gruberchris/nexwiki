package main

import (
	"testing"

	// This will import the server package and make its functions available
	_ "nexwiki/server"
)

// Test that demonstrates the archived tag functionality
func TestMainFunctionality(t *testing.T) {
	// This test is just to verify that the imports work correctly
	// The actual archived tag tests are in server/archived_tag_test.go
	t.Log("Server package imported successfully")
}
