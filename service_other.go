//go:build !windows

package main

func maybeRunOSService(_ *ServerConfig) (bool, error) {
	return false, nil
}
