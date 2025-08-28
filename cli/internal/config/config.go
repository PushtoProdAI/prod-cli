package config

import "strings"

var (
	SupabaseURL     = "http://localhost:54321"
	SupabaseAnonKey = ""
	ProdDebug       = "false"
)

func DebugMode() bool {
	return strings.ToLower(ProdDebug) == "true"
}
