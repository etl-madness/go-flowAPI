//go:build !windows

package main

func lookupGroupSidQualified(gid string) []string {
	return nil
}
