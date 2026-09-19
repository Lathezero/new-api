package billing_setting

import (
	"maps"
	"strings"

	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

// GroupModelPricingOption is the options-table key storing per-(model, group)
// pricing overrides as map[model]map[group]GroupPricing.
const GroupModelPricingOption = "billing_setting.group_model_pricing"

// GroupPricing is one group-specific pricing override for a model. Fields left
// nil/empty inherit the model's global pricing. JSON keys match the model
// pricing editor's PricingValues keys so the stored option value round-trips
// between the typed runtime cache and the untyped editor payload.
type GroupPricing struct {
	Mode                 string   `json:"billing_setting.billing_mode,omitempty"`
	Expr                 string   `json:"billing_setting.billing_expr,omitempty"`
	ModelPrice           *float64 `json:"ModelPrice,omitempty"`
	ModelRatio           *float64 `json:"ModelRatio,omitempty"`
	CompletionRatio      *float64 `json:"CompletionRatio,omitempty"`
	CacheRatio           *float64 `json:"CacheRatio,omitempty"`
	CreateCacheRatio     *float64 `json:"CreateCacheRatio,omitempty"`
	ImageRatio           *float64 `json:"ImageRatio,omitempty"`
	AudioRatio           *float64 `json:"AudioRatio,omitempty"`
	AudioCompletionRatio *float64 `json:"AudioCompletionRatio,omitempty"`
}

// GetGroupModelPricing returns the pricing override configured for
// (model, group). The model name goes through the same wildcard normalization
// as the global price lookups.
func GetGroupModelPricing(model, group string) (GroupPricing, bool) {
	if group == "" {
		return GroupPricing{}, false
	}
	groups, ok := billingSetting.GroupModelPricing[ratio_setting.FormatMatchingModelName(model)]
	if !ok {
		return GroupPricing{}, false
	}
	pricing, ok := groups[group]
	return pricing, ok
}

// GetGroupBillingMode resolves the billing mode for (model, group). A group
// override with an explicit mode wins; otherwise the mode is inferred from the
// override's fields, falling back to the model's global mode.
func GetGroupBillingMode(model, group string) string {
	pricing, ok := GetGroupModelPricing(model, group)
	if ok {
		if pricing.Mode != "" {
			return pricing.Mode
		}
		if strings.TrimSpace(pricing.Expr) != "" {
			return BillingModeTieredExpr
		}
		if pricing.ModelPrice != nil || pricing.ModelRatio != nil {
			return BillingModeRatio
		}
	}
	return GetBillingMode(model)
}

// GetGroupBillingExpr returns the group override expression when set,
// otherwise the model's global (or built-in) expression.
func GetGroupBillingExpr(model, group string) (string, bool) {
	if pricing, ok := GetGroupModelPricing(model, group); ok && strings.TrimSpace(pricing.Expr) != "" {
		return pricing.Expr, true
	}
	return GetBillingExpr(model)
}

// ResolveTaskBillingExprForGroup orders task expression resolution as:
// plugin override → group override → model expression → mapped model. A group
// override that resolves to ratio billing disables expression billing for the
// group entirely, keeping the task path consistent with GetGroupBillingMode.
func ResolveTaskBillingExprForGroup(pluginKey, model, mappedModel, group string) (string, bool) {
	if _, hasOverride := GetGroupModelPricing(model, group); hasOverride &&
		GetGroupBillingMode(model, group) != BillingModeTieredExpr {
		return "", false
	}
	if pluginKey != "" {
		if expr, ok := GetPluginBillingExpr(pluginKey, model); ok {
			return expr, true
		}
		if mappedModel != "" && mappedModel != model {
			if expr, ok := GetPluginBillingExpr(pluginKey, mappedModel); ok {
				return expr, true
			}
		}
	}
	if pricing, ok := GetGroupModelPricing(model, group); ok && strings.TrimSpace(pricing.Expr) != "" {
		return pricing.Expr, true
	}
	if GetBillingMode(model) == BillingModeTieredExpr {
		return GetBillingExpr(model)
	}
	if mappedModel != "" && mappedModel != model && GetBillingMode(mappedModel) == BillingModeTieredExpr {
		expression, ok := GetBillingExpr(mappedModel)
		return expression, ok && strings.TrimSpace(expression) != ""
	}
	return "", false
}

// Group-aware numeric field accessors. Each returns the group override when
// configured, otherwise the model's global value.

func GetGroupCompletionRatio(model, group string) float64 {
	if pricing, ok := GetGroupModelPricing(model, group); ok && pricing.CompletionRatio != nil {
		return *pricing.CompletionRatio
	}
	return ratio_setting.GetCompletionRatio(model)
}

func GetGroupAudioRatio(model, group string) float64 {
	if pricing, ok := GetGroupModelPricing(model, group); ok && pricing.AudioRatio != nil {
		return *pricing.AudioRatio
	}
	return ratio_setting.GetAudioRatio(model)
}

func GetGroupAudioCompletionRatio(model, group string) float64 {
	if pricing, ok := GetGroupModelPricing(model, group); ok && pricing.AudioCompletionRatio != nil {
		return *pricing.AudioCompletionRatio
	}
	return ratio_setting.GetAudioCompletionRatio(model)
}

// GetGroupModelRatio overlays the group model-ratio override on the global
// lookup, mirroring GetModelRatio's return contract.
func GetGroupModelRatio(model, group string) (float64, bool, string) {
	ratio, ok, matchName := ratio_setting.GetModelRatio(model)
	if pricing, exists := GetGroupModelPricing(model, group); exists && pricing.ModelRatio != nil {
		return *pricing.ModelRatio, true, matchName
	}
	return ratio, ok, matchName
}

// GetGroupModelPrice overlays the group fixed-price override on the global
// lookup. A group ratio override suppresses a global fixed price so the group
// bills by ratio instead.
func GetGroupModelPrice(model, group string, printErr bool) (float64, bool) {
	if pricing, ok := GetGroupModelPricing(model, group); ok {
		if pricing.ModelPrice != nil {
			return *pricing.ModelPrice, true
		}
		if pricing.ModelRatio != nil {
			return -1, false
		}
	}
	return ratio_setting.GetModelPrice(model, printErr)
}

// GetGroupModelPricingCopy returns a deep copy for settings sync and the
// pricing snapshot API.
func GetGroupModelPricingCopy() map[string]map[string]GroupPricing {
	result := make(map[string]map[string]GroupPricing, len(billingSetting.GroupModelPricing))
	for model, groups := range billingSetting.GroupModelPricing {
		copied := make(map[string]GroupPricing, len(groups))
		maps.Copy(copied, groups)
		result[model] = copied
	}
	return result
}
