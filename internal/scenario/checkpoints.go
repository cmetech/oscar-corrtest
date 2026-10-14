package scenario

import (
	"fmt"
	"strings"
	"time"
)

func validateCheckpoints(item Case, budget time.Duration) error {
	if len(item.Checkpoints) > MaxCheckpoints {
		return fmt.Errorf("case %q exceeds checkpoint budget", item.Name)
	}
	count := len(item.Events)
	if count == 0 {
		count = item.Repeat
	}
	names := map[string]bool{}
	previous := 0
	for _, point := range item.Checkpoints {
		if point.Name == "" || strings.TrimSpace(point.Name) != point.Name || len(point.Name) > MaxCaseNameLength || names[point.Name] {
			return fmt.Errorf("case %q checkpoint name is invalid or duplicated", item.Name)
		}
		names[point.Name] = true
		if point.AfterEvent <= previous || point.AfterEvent > count {
			return fmt.Errorf("case %q checkpoint ordinals must increase within its events", item.Name)
		}
		previous = point.AfterEvent
		if point.Window <= 0 || point.Window > MaxCaseWindow {
			return fmt.Errorf("checkpoint %q window is invalid", point.Name)
		}
		at := time.Duration(0)
		if len(item.Events) > 0 {
			at = item.Events[point.AfterEvent-1].Delay
		}
		if at+point.Window > budget || at+point.Window > item.Window+15*time.Second {
			return fmt.Errorf("checkpoint %q exceeds observation budget", point.Name)
		}
		if point.AfterEvent < count {
			next := time.Duration(0)
			if len(item.Events) > 0 {
				next = item.Events[point.AfterEvent].Delay
			}
			if at+point.Window > next {
				return fmt.Errorf("checkpoint %q overlaps the next input", point.Name)
			}
		}
		if err := validateAssertions(point.Name, point.Assertions); err != nil {
			return err
		}
	}
	return nil
}
