package workflow

import (
	"github.com/nandu343/real-soc/internal/models"
)

// deepMergeMaps recursively merges src into dst and returns dst. For keys
// present in both maps: if both values are maps, merge them recursively;
// otherwise src value overwrites dst.
func deepMergeMaps(dst, src map[string]any) map[string]any {
	if dst == nil {
		// make a shallow copy of src
		out := make(map[string]any, len(src))
		for k, v := range src {
			out[k] = v
		}
		return out
	}
	for k, v := range src {
		if vmap, ok := v.(map[string]any); ok {
			if existing, ok2 := dst[k].(map[string]any); ok2 {
				dst[k] = deepMergeMaps(existing, vmap)
				continue
			}
			// overwrite non-map with map
			dst[k] = deepMergeMaps(nil, vmap)
			continue
		}
		// non-map values override
		dst[k] = v
	}
	return dst
}

// MergeWorkflowStates merges the contents of updated into base state.
// Specifically, it merges Task outputs by TaskID (falling back to Name),
// preserves previous outputs, and overlays new values from updated.
// It mutates and returns base.
func MergeWorkflowStates(base *models.WorkflowState, updated *models.WorkflowState) *models.WorkflowState {
	if base == nil {
		return updated
	}
	if updated == nil {
		return base
	}

	// Merge tasks: match by TaskID first, then by Name.
	for _, ut := range updated.Tasks {
		var bt *models.TaskObject
		for i := range base.Tasks {
			if base.Tasks[i].TaskID != "" && ut.TaskID != "" && base.Tasks[i].TaskID == ut.TaskID {
				bt = &base.Tasks[i]
				break
			}
			if bt == nil && base.Tasks[i].Name == ut.Name {
				bt = &base.Tasks[i]
				// don't break — prefer TaskID match if later
			}
		}
		if bt == nil {
			// unknown task — append
			base.Tasks = append(base.Tasks, ut)
			continue
		}

		// Merge Output maps
		if ut.Output == nil {
			// nothing to merge
		} else if bt.Output == nil {
			// copy updated output
			bt.Output = make(map[string]any, len(ut.Output))
			for k, v := range ut.Output {
				bt.Output[k] = v
			}
		} else {
			bt.Output = deepMergeMaps(bt.Output, ut.Output)
		}

		// Overwrite status and other simple fields from updated task if present
		if ut.Status != "" {
			bt.Status = ut.Status
		}
		if ut.Error != "" {
			bt.Error = ut.Error
		}
		if ut.Next != "" {
			bt.Next = ut.Next
		}
	}

	// Merge artifacts by appending new ones
	if len(updated.Artifacts) > 0 {
		base.Artifacts = append(base.Artifacts, updated.Artifacts...)
	}

	// prefer updated's timestamps/status if set
	if !updated.UpdatedAt.IsZero() {
		base.UpdatedAt = updated.UpdatedAt
	}
	if updated.Status != "" {
		base.Status = updated.Status
	}

	return base
}
