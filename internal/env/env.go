// Package env reads XDS_* environment variables used as flag defaults.
package env

import (
	"net"
	"os"
	"strconv"
)

// String returns the value of key, or def if it's unset or empty.
func String(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// Bool returns key parsed as a bool, or def if it's unset or not a bool.
func Bool(key string, def bool) bool {
	if b, err := strconv.ParseBool(os.Getenv(key)); err == nil {
		return b
	}
	return def
}

// Addr joins the ipKey and portKey variables into a listen address. If
// neither is set it returns def; a missing half is taken from def.
func Addr(ipKey, portKey, def string) string {
	ip, port := os.Getenv(ipKey), os.Getenv(portKey)
	if ip == "" && port == "" {
		return def
	}
	defIP, defPort, _ := net.SplitHostPort(def)
	return net.JoinHostPort(String(ipKey, defIP), String(portKey, defPort))
}

// Source returns the config source for one xDS API: "" when skipKey is
// true, else fileKey, else urlKey, else def.
func Source(skipKey, fileKey, urlKey, def string) string {
	if Bool(skipKey, false) {
		return ""
	}
	if v := os.Getenv(fileKey); v != "" {
		return v
	}
	return String(urlKey, def)
}
