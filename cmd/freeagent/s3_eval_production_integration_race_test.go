//go:build race

package main

import "time"

func s3CProductionTestTimeout() time.Duration {
	return 180 * time.Second
}
