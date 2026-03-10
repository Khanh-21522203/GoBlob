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

func parseKeyValuePairs(values []string) (map[string]string, error) {
	out := make(map[string]string, len(values))
	for _, raw := range values {
		pair := strings.TrimSpace(raw)
		if pair == "" {
			continue
		}
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid key=value pair %q", pair)
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if key == "" {
			return nil, fmt.Errorf("empty key in pair %q", pair)
		}
		out[key] = value
	}
	return out, nil
}
