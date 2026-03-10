package command

import (
	"fmt"
	"strings"
)

type stringSliceFlag struct {
	values *[]string
}

func newStringSliceFlag(values *[]string) *stringSliceFlag {
	return &stringSliceFlag{values: values}
}

func (f *stringSliceFlag) String() string {
	if f == nil || f.values == nil {
		return ""
	}
	return strings.Join(*f.values, ",")
}

func (f *stringSliceFlag) Set(value string) error {
	if f == nil || f.values == nil {
		return fmt.Errorf("invalid string slice flag")
	}
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	*f.values = append(*f.values, trimmed)
	return nil
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func bindAddress(host string, port int) string {
	if strings.TrimSpace(host) == "" || host == "0.0.0.0" {
		return fmt.Sprintf(":%d", port)
	}
	return fmt.Sprintf("%s:%d", host, port)
}
