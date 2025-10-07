package tokens

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"text/template"

	"github.com/go-errors/errors"
)

// RulesEngine calculates token costs based on configurable rules
type RulesEngine struct {
	// Rules are loaded from database or config
	rules map[string]*TokenRule
}

// NewRulesEngine creates a rules engine with default rules
func NewRulesEngine() *RulesEngine {
	return &RulesEngine{
		rules: getDefaultRules(),
	}
}

// CalculateCost calculates the token cost for an operation
func (re *RulesEngine) CalculateCost(operation string, metadata Metadata) (int, error) {
	rule, ok := re.rules[operation]
	if !ok {
		return 0, errors.Errorf("no rule found for operation: %s", operation)
	}

	// Start with base cost
	cost := rule.BaseCost

	// Apply multipliers based on conditions
	for _, multiplier := range rule.Multipliers {
		matches, err := re.evaluateCondition(multiplier.Condition, metadata)
		if err != nil {
			// Log error but continue
			continue
		}
		if matches {
			cost *= multiplier.Factor
		}
	}

	// Round up to nearest integer
	return int(cost + 0.5), nil
}

// CalculateCostWithBreakdown returns cost and human-readable breakdown
func (re *RulesEngine) CalculateCostWithBreakdown(operation string, metadata Metadata) (int, []string, error) {
	rule, ok := re.rules[operation]
	if !ok {
		return 0, nil, errors.Errorf("no rule found for operation: %s", operation)
	}

	breakdown := []string{}
	cost := rule.BaseCost
	breakdown = append(breakdown, fmt.Sprintf("Base cost: %.2f tokens", cost))

	// Apply multipliers and track which ones applied
	for _, multiplier := range rule.Multipliers {
		matches, err := re.evaluateCondition(multiplier.Condition, metadata)
		if err != nil {
			continue
		}
		if matches {
			cost *= multiplier.Factor
			breakdown = append(breakdown, fmt.Sprintf("• %s: ×%.2f", multiplier.Description, multiplier.Factor))
		}
	}

	finalCost := int(cost + 0.5)
	breakdown = append(breakdown, fmt.Sprintf("Total: %d token%s", finalCost, pluralize(finalCost)))

	return finalCost, breakdown, nil
}

// evaluateCondition evaluates a Go template expression as a boolean
// The condition is a Go template that should evaluate to "true" or "false"
// Example conditions:
//   - {{ gt .services_count 3 }}
//   - {{ eq .llm_model "gpt-4" }}
//   - {{ and (eq .platform "render") (gt .services_count 1) }}
func (re *RulesEngine) evaluateCondition(condition string, metadata Metadata) (bool, error) {
	// Create a template with the condition
	tmpl, err := template.New("condition").Parse(condition)
	if err != nil {
		return false, errors.WrapPrefix(err, "failed to parse condition template", 0)
	}

	// Execute template with metadata
	var result strings.Builder
	if err := tmpl.Execute(&result, metadata); err != nil {
		return false, errors.WrapPrefix(err, "failed to execute condition template", 0)
	}

	// Parse result as boolean
	resultStr := strings.TrimSpace(result.String())

	// Handle boolean string results
	switch resultStr {
	case "true", "TRUE", "1":
		return true, nil
	case "false", "FALSE", "0", "":
		return false, nil
	default:
		// Try to parse as boolean
		boolVal, err := strconv.ParseBool(resultStr)
		if err != nil {
			return false, errors.Errorf("condition result is not a boolean: %s", resultStr)
		}
		return boolVal, nil
	}
}

// getDefaultRules returns the default token cost rules
// NOTE: These are the source of truth for pricing. In the future, we could
// load rules from the database for dynamic pricing via an admin UI.
func getDefaultRules() map[string]*TokenRule {
	return map[string]*TokenRule{
		OperationDeploy: {
			Operation: OperationDeploy,
			BaseCost:  1.0,
			Multipliers: []CostMultiplier{
				{
					Condition:   `{{ gt .services_count 3 }}`,
					Factor:      1.5,
					Description: "Multi-service deployment",
				},
				{
					Condition:   `{{ eq .llm_model "gpt-4" }}`,
					Factor:      1.3,
					Description: "Premium LLM model (GPT-4)",
				},
				{
					Condition:   `{{ eq .llm_model "claude-3-opus" }}`,
					Factor:      1.3,
					Description: "Premium LLM model (Claude Opus)",
				},
			},
			Priority: 100,
			Active:   true,
		},
		OperationDryRun: {
			Operation:   OperationDryRun,
			BaseCost:    0.5,
			Multipliers: []CostMultiplier{},
			Priority:    90,
			Active:      true,
		},
		OperationRollback: {
			Operation:   OperationRollback,
			BaseCost:    0.25,
			Multipliers: []CostMultiplier{},
			Priority:    80,
			Active:      true,
		},
		OperationStatus: {
			Operation:   OperationStatus,
			BaseCost:    0.1,
			Multipliers: []CostMultiplier{},
			Priority:    70,
			Active:      true,
		},
	}
}

// LoadRulesFromDatabase loads rules from the database (future enhancement)
// Currently not implemented - rules are defined in getDefaultRules() for simplicity.
// This could be implemented in the future when we add an admin UI for dynamic pricing.
func (re *RulesEngine) LoadRulesFromDatabase(ctx context.Context, client *Client) error {
	return errors.Errorf("not implemented - rules are currently defined in Go code")
}

// AddRule adds or updates a rule in the engine
func (re *RulesEngine) AddRule(rule *TokenRule) {
	re.rules[rule.Operation] = rule
}

// GetRule returns a rule for an operation
func (re *RulesEngine) GetRule(operation string) (*TokenRule, bool) {
	rule, ok := re.rules[operation]
	return rule, ok
}

// ValidateOperation checks if an operation is valid
func ValidateOperation(operation string) bool {
	validOps := []string{
		OperationDeploy,
		OperationDryRun,
		OperationRollback,
		OperationStatus,
	}

	for _, op := range validOps {
		if operation == op {
			return true
		}
	}
	return false
}
