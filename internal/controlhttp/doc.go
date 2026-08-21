// Package controlhttp implements the optional local Control API's bounded,
// authenticated HTTP transport. It owns neither a listener nor application
// state: composition supplies the exact listener authority, process-local
// session registry, cursor codec, and narrow application services.
package controlhttp
