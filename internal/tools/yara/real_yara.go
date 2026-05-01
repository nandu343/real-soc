//go:build yara
// +build yara

package yara

import (
	"context"
	"fmt"
	"io/ioutil"

	gyara "github.com/hillu/go-yara/v4"
)

// This file is compiled only when building with the `yara` build tag. It
// provides a thin wrapper around the go-yara bindings and implements the
// Engine interface using libyara.

type cgoEngine struct {
	rules *gyara.Rules
}

func NewEngine() Engine { return &cgoEngine{} }

func (e *cgoEngine) CompileRulesFromFile(path string) error {
	b, err := ioutil.ReadFile(path)
	if err != nil {
		return err
	}
	comp, err := gyara.NewCompiler()
	if err != nil {
		return err
	}
	if err := comp.AddString(string(b), ""); err != nil {
		return err
	}
	rules, err := comp.GetRules()
	if err != nil {
		return err
	}
	e.rules = rules
	return nil
}

func (e *cgoEngine) CompileRulesFromString(content string) error {
	comp, err := gyara.NewCompiler()
	if err != nil {
		return err
	}
	if err := comp.AddString(content, ""); err != nil {
		return err
	}
	rules, err := comp.GetRules()
	if err != nil {
		return err
	}
	e.rules = rules
	return nil
}

func (e *cgoEngine) ScanFile(ctx context.Context, path string) (ScanResult, error) {
	if e.rules == nil {
		return ScanResult{}, fmt.Errorf("no rules compiled")
	}
	scanner, err := gyara.NewScanner(e.rules)
	if err != nil {
		return ScanResult{}, err
	}
	var matches gyara.MatchRules
	scanner.SetCallback(&matches)
	if err := scanner.ScanFile(path); err != nil {
		return ScanResult{}, err
	}
	return convertMatches(matches), nil
}

func (e *cgoEngine) ScanBytes(ctx context.Context, data []byte) (ScanResult, error) {
	if e.rules == nil {
		return ScanResult{}, fmt.Errorf("no rules compiled")
	}
	scanner, err := gyara.NewScanner(e.rules)
	if err != nil {
		return ScanResult{}, err
	}
	var matches gyara.MatchRules
	scanner.SetCallback(&matches)
	if err := scanner.ScanMem(data); err != nil {
		return ScanResult{}, err
	}
	return convertMatches(matches), nil
}

func convertMatches(m gyara.MatchRules) (r ScanResult) {
	out := make([]Match, 0, len(m))
	var total int64
	for _, mr := range m {
		mm := Match{
			Rule:      mr.Rule,
			Namespace: mr.Namespace,
			Tags:      mr.Tags,
			Meta:      map[string]string{},
			Strings:   []string{},
		}
		for k, v := range mr.Meta {
			mm.Meta[k] = fmt.Sprintf("%v", v)
		}
		for _, s := range mr.Matches {
			mm.Strings = append(mm.Strings, s.Data)
			total += int64(len(s.Data))
		}
		out = append(out, mm)
	}
	r.Matches = out
	r.ScannedBytes = total
	r.Status = "ok"
	return r
}
