package workflow

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/nandu343/real-soc/internal/models"
)

// ResolveDotReference resolves expressions of the form:
//
//	"task name".output.some.path
//	taskName.output.some.path
//
// It searches state.Tasks for a matching TaskObject by Name or TaskID
// and returns the nested value if present.
func ResolveDotReference(ref string, state *models.WorkflowState) (any, error) {
	s := strings.TrimSpace(ref)
	if s == "" {
		return nil, errors.New("empty reference")
	}

	// locate the left-hand task identifier (may be quoted)
	var taskName string
	var rest string
	if strings.HasPrefix(s, "\"") {
		// quoted name
		end := strings.Index(s[1:], "\"")
		if end == -1 {
			return nil, fmt.Errorf("unterminated quoted task name in %q", ref)
		}
		taskName = s[1 : 1+end]
		rest = strings.TrimPrefix(s[1+end+1:], ".")
	} else {
		// unquoted — split at first dot
		parts := strings.SplitN(s, ".", 2)
		taskName = parts[0]
		if len(parts) > 1 {
			rest = parts[1]
		} else {
			rest = ""
		}
	}

	if rest == "" {
		return nil, fmt.Errorf("reference %q missing \".output\" segment", ref)
	}

	// expect rest to start with 'output'
	if !strings.HasPrefix(rest, "output") {
		return nil, fmt.Errorf("unsupported reference %q, expected '.output'", ref)
	}

	path := ""
	if len(rest) > len("output") {
		// skip leading 'output' and possible dot
		p := rest[len("output"):]
		if strings.HasPrefix(p, ".") {
			path = p[1:]
		} else if p == "" {
			path = ""
		} else {
			// unexpected
			path = p
		}
	}

	// find task by Name (preferred) or TaskID
	var found *models.TaskObject
	for i := range state.Tasks {
		t := &state.Tasks[i]
		if t.Name == taskName || t.TaskID == taskName {
			found = t
			break
		}
	}
	if found == nil {
		return nil, fmt.Errorf("task %q not found in state", taskName)
	}
	if found.Output == nil {
		return nil, fmt.Errorf("task %q has no output", taskName)
	}

	if path == "" {
		return found.Output, nil
	}

	parts := strings.Split(path, ".")
	var cur any = found.Output
	for _, p := range parts {
		if cur == nil {
			return nil, fmt.Errorf("path %q not found (nil encountered)", ref)
		}
		switch v := cur.(type) {
		case map[string]any:
			if val, ok := v[p]; ok {
				cur = val
				continue
			}
			return nil, fmt.Errorf("key %q not found in output for %q", p, taskName)
		case []any:
			// allow numeric index
			idx, err := strconv.Atoi(p)
			if err != nil {
				return nil, fmt.Errorf("invalid array index %q in %q: %w", p, ref, err)
			}
			if idx < 0 || idx >= len(v) {
				return nil, fmt.Errorf("array index %d out of range in %q", idx, ref)
			}
			cur = v[idx]
		default:
			return nil, fmt.Errorf("cannot descend into non-container value at %q", p)
		}
	}

	return cur, nil
}

// ExpandInputs scans a task's Input map and replaces any string values that
// reference previous task outputs using the dot-notation with the resolved
// value from the WorkflowState. It mutates the Input map in-place.
func ExpandInputs(t *models.TaskObject, state *models.WorkflowState) error {
	if t == nil || t.Input == nil {
		return nil
	}
	for k, v := range t.Input {
		switch s := v.(type) {
		case string:
			// quick heuristic: contains ".output"
			if strings.Contains(s, ".output") {
				// attempt resolve
				val, err := ResolveDotReference(s, state)
				if err != nil {
					// don't fail hard — return error so caller can decide
					return fmt.Errorf("expand input key %q: %w", k, err)
				}
				t.Input[k] = val
			}
		case map[string]any:
			// recursively expand nested maps
			// convert to TaskObject-like temporary
			// walk keys and handle strings
			for kk, vv := range s {
				if ss, ok := vv.(string); ok && strings.Contains(ss, ".output") {
					val, err := ResolveDotReference(ss, state)
					if err != nil {
						return fmt.Errorf("expand input %q.%q: %w", k, kk, err)
					}
					s[kk] = val
				}
			}
			t.Input[k] = s
		case []any:
			// walk array elements
			for i, vv := range s {
				if ss, ok := vv.(string); ok && strings.Contains(ss, ".output") {
					val, err := ResolveDotReference(ss, state)
					if err != nil {
						return fmt.Errorf("expand input %q[%d]: %w", k, i, err)
					}
					s[i] = val
				}
			}
			t.Input[k] = s
		default:
			// leave other types unchanged
		}
	}
	return nil
}
