package version

// Version is set at build time via -ldflags.
// Example: -X github.com/seer-monitoring/SEER/server/internal/version.Version=v1.2.3
var Version = "dev"
