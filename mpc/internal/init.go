package init

import "os"

func init() {
	// Setting this to remove warning
	// from github.com/ldsec/unlynx/lib
	if _, ok := os.LookupEnv("CONN_TIMEOUT"); !ok {
		os.Setenv("CONN_TIMEOUT", "10m0s")
	}
}
