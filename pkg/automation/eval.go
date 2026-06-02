package automation

import (
	"fmt"
	"regexp"
	"strings"
)

var varRegex = regexp.MustCompile(`\$\{([a-zA-Z0-9_.]+)\}`)

// Interpolate substitutes ${var_name} placeholders in a string with values from the vars map.
func Interpolate(str string, vars map[string]interface{}) string {
	if str == "" {
		return ""
	}
	return varRegex.ReplaceAllStringFunc(str, func(match string) string {
		name := match[2 : len(match)-1]
		val, exists := lookupVar(name, vars)
		if !exists {
			return match
		}
		return fmt.Sprintf("%v", val)
	})
}

// lookupVar traverses a dotted path (e.g. "a.b.c") to find a value in a nested map.
func lookupVar(path string, vars map[string]interface{}) (interface{}, bool) {
	parts := strings.Split(path, ".")
	var current interface{} = vars
	for _, part := range parts {
		m, ok := current.(map[string]interface{})
		if !ok {
			return nil, false
		}
		val, exists := m[part]
		if !exists {
			return nil, false
		}
		current = val
	}
	return current, true
}

// evaluateValue parses and resolves basic math and type conversions for assigned values.
func evaluateValue(val interface{}, vars map[string]interface{}) interface{} {
	s, ok := val.(string)
	if !ok {
		return val
	}
	interpolated := Interpolate(s, vars)
	interpolated = strings.TrimSpace(interpolated)

	// Basic binary math operations: +, -, *, /
	operators := []string{"+", "-", "*", "/"}
	for _, op := range operators {
		idx := strings.Index(interpolated, op)
		if idx != -1 {
			leftStr := strings.TrimSpace(interpolated[:idx])
			rightStr := strings.TrimSpace(interpolated[idx+len(op):])

			var left, right float64
			if _, err1 := fmt.Sscan(leftStr, &left); err1 == nil {
				if _, err2 := fmt.Sscan(rightStr, &right); err2 == nil {
					switch op {
					case "+":
						return left + right
					case "-":
						return left - right
					case "*":
						return left * right
					case "/":
						if right != 0 {
							return left / right
						}
					}
				}
			}
		}
	}

	// Type parsing
	var floatVal float64
	if _, err := fmt.Sscan(interpolated, &floatVal); err == nil {
		return floatVal
	}
	if interpolated == "true" {
		return true
	}
	if interpolated == "false" {
		return false
	}
	return interpolated
}

// evaluateCondition evaluates a comparison string (e.g., "${counter} < 5") to a boolean value.
func evaluateCondition(cond string, vars map[string]interface{}) (bool, error) {
	if cond == "" {
		return true, nil
	}
	interpolated := Interpolate(cond, vars)
	interpolated = strings.TrimSpace(interpolated)

	operators := []string{"<=", ">=", "==", "!=", "<", ">"}
	for _, op := range operators {
		idx := strings.Index(interpolated, op)
		if idx != -1 {
			leftStr := strings.TrimSpace(interpolated[:idx])
			rightStr := strings.TrimSpace(interpolated[idx+len(op):])

			// Try numeric comparison
			var leftNum, rightNum float64
			_, errL := fmt.Sscan(leftStr, &leftNum)
			_, errR := fmt.Sscan(rightStr, &rightNum)
			if errL == nil && errR == nil {
				switch op {
				case "<":
					return leftNum < rightNum, nil
				case ">":
					return leftNum > rightNum, nil
				case "<=":
					return leftNum <= rightNum, nil
				case ">=":
					return leftNum >= rightNum, nil
				case "==":
					return leftNum == rightNum, nil
				case "!=":
					return leftNum != rightNum, nil
				}
			}

			// Try boolean comparison
			if (leftStr == "true" || leftStr == "false") && (rightStr == "true" || rightStr == "false") {
				leftBool := leftStr == "true"
				rightBool := rightStr == "true"
				switch op {
				case "==":
					return leftBool == rightBool, nil
				case "!=":
					return leftBool != rightBool, nil
				}
			}

			// String comparison
			leftStr = strings.Trim(leftStr, `"'`)
			rightStr = strings.Trim(rightStr, `"'`)
			switch op {
			case "==":
				return leftStr == rightStr, nil
			case "!=":
				return leftStr != rightStr, nil
			}
		}
	}

	if interpolated == "true" {
		return true, nil
	}
	if interpolated == "false" {
		return false, nil
	}
	return false, fmt.Errorf("invalid condition expression: %q", cond)
}

// InterpolateStep substitutes placeholders in all compatible string fields of a Step.
func InterpolateStep(step Step, vars map[string]interface{}) Step {
	step.Text = Interpolate(step.Text, vars)
	step.Keys = Interpolate(step.Keys, vars)
	step.Duration = Interpolate(step.Duration, vars)
	step.Image = Interpolate(step.Image, vars)
	step.Timeout = Interpolate(step.Timeout, vars)
	step.Target = Interpolate(step.Target, vars)
	step.Command = Interpolate(step.Command, vars)
	step.Message = Interpolate(step.Message, vars)
	step.Title = Interpolate(step.Title, vars)
	step.Output = Interpolate(step.Output, vars)
	step.Region = Interpolate(step.Region, vars)
	step.Language = Interpolate(step.Language, vars)
	step.Model = Interpolate(step.Model, vars)
	step.When = Interpolate(step.When, vars)
	step.Color = Interpolate(step.Color, vars)
	if step.Window != nil {
		winCopy := *step.Window
		winCopy.Title = Interpolate(winCopy.Title, vars)
		winCopy.Class = Interpolate(winCopy.Class, vars)
		step.Window = &winCopy
	}
	if s, ok := step.X.(string); ok {
		step.X = Interpolate(s, vars)
	}
	if s, ok := step.Y.(string); ok {
		step.Y = Interpolate(s, vars)
	}
	return step
}

// getIntField converts interface{} coordinate inputs to integers.
func getIntField(val interface{}, vars map[string]interface{}, defaultVal int) int {
	if val == nil {
		return defaultVal
	}
	if i, ok := val.(int); ok {
		return i
	}
	if f, ok := val.(float64); ok {
		return int(f)
	}
	if s, ok := val.(string); ok {
		interpolated := Interpolate(s, vars)
		var parsedVal float64
		if _, err := fmt.Sscan(interpolated, &parsedVal); err == nil {
			return int(parsedVal)
		}
	}
	return defaultVal
}
