package main

import (
	"flag"
	"fmt"
	"io"
)

type listFilter struct {
	kind   string
	fields map[string]string
}

func parseList(args []string) (*listFilter, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("list sessions|jobs|audit")
	}
	f := &listFilter{kind: args[1], fields: map[string]string{}}
	fs := flag.NewFlagSet("list "+f.kind, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var allowed map[string]string
	switch f.kind {
	case "jobs":
		allowed = map[string]string{"session-id": "session_id", "request-id": "request_id", "status": "status"}
	case "sessions":
		allowed = map[string]string{"name": "name", "environment": "environment", "state": "state"}
	case "audit":
		allowed = map[string]string{}
	default:
		return nil, fmt.Errorf("unknown list type")
	}
	values := map[string]*string{}
	for name := range allowed {
		values[name] = fs.String(name, "", "")
	}
	if err := fs.Parse(args[2:]); err != nil {
		return nil, err
	}
	if len(fs.Args()) != 0 {
		return nil, fmt.Errorf("unexpected list arguments")
	}
	var empty string
	fs.Visit(func(fl *flag.Flag) {
		if *values[fl.Name] == "" {
			empty = fl.Name
		}
		f.fields[allowed[fl.Name]] = *values[fl.Name]
	})
	if empty != "" {
		return nil, fmt.Errorf("--%s must not be empty", empty)
	}
	for key, valid := range map[string]map[string]bool{
		"status": {"pending": true, "queued": true, "dispatched": true, "running": true, "completed": true, "denied": true, "cancelled": true, "unknown": true},
		"state":  {"pending": true, "active": true, "expired": true, "detached": true, "blocked": true},
	} {
		if value, ok := f.fields[key]; ok && !valid[value] {
			return nil, fmt.Errorf("invalid %s: %s", key, value)
		}
	}
	return f, nil
}

func (f *listFilter) apply(items []map[string]any) []map[string]any {
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		match := true
		for key, value := range f.fields {
			actual, ok := item[key].(string)
			if !ok || actual != value {
				match = false
				break
			}
		}
		if match {
			result = append(result, item)
		}
	}
	return result
}
