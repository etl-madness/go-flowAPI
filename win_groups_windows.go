//go:build windows

package main

import (
	"strings"
	"syscall"
)

func lookupGroupSidQualified(gid string) []string {
	sid, err := syscall.StringToSid(strings.TrimSpace(gid))
	if err != nil || sid == nil {
		return nil
	}
	account, domain, _, err := sid.LookupAccount("")
	if err != nil || strings.TrimSpace(account) == "" {
		return nil
	}
	account = strings.TrimSpace(account)
	domain = strings.TrimSpace(domain)
	if domain != "" {
		return []string{domain + `\` + account, account}
	}
	return []string{account}
}
