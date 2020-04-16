package utils

import (
	// #nosec G505
	"crypto/sha1"
	"fmt"
	"io"
)

// LengthName creates a string of maximum defined length.
func LengthName(maxLen int, hashPrefix string, format string, a ...interface{}) (string, error) {
	friendlyName := fmt.Sprintf(format, a...)
	if len(friendlyName) < maxLen {
		return friendlyName, nil
	}

	// If that's too long, just hash the name. It's not very user friendly, but whatever
	//
	// We can suppress the gosec warning about sha1 here because we don't use sha1 for crypto
	// purposes, but only as a string shortener
	// #nosec G401
	hasher := sha1.New()
	io.WriteString(hasher, friendlyName)
	hashedName := hashPrefix + fmt.Sprintf("%x", hasher.Sum(nil))

	if len(hashedName) >= maxLen {
		return "", fmt.Errorf("Cannot shorten '%s' with prefix %s", friendlyName, hashPrefix)
	}
	return hashedName, nil
}

func DNSLengthName(hashPrefix string, format string, a ...interface{}) string {
	const maxDNSLen = 64

	// TODO(jaosorior): Handle error
	name, _ := LengthName(maxDNSLen, hashPrefix, format, a...)
	return name
}
