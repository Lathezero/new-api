package model

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"maps"
	"math"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// PricingValues is one model's configuration, keyed by the existing option
// names. A missing key inherits the engine's default; an explicit zero is free.
type PricingValues map[string]any

type ModelPricingChange struct {
	ModelName       string        `json:"model_name"`
	ExpectedVersion string        `json:"expected_version"`
	Pricing         PricingValues `json:"pricing"`
	Reset           bool          `json:"reset,omitempty"`
}

// ModelPricingGroupEntry is one group's pricing override for a model:
// the raw configured values and the effective view after overlaying them on
// the model's global pricing.
type ModelPricingGroupEntry struct {
	Configured PricingValues `json:"configured"`
	Effective  PricingValues `json:"effective"`
}

type ModelPricingEntry struct {
	ModelPricingDescription
	PluginVariants []ModelPricingPluginVariant          `json:"plugin_variants,omitempty"`
	ModelName      string                               `json:"model_name"`
	Version        string                               `json:"version"`
	Configured     PricingValues                        `json:"configured"`
	Groups         map[string]ModelPricingGroupEntry    `json:"groups,omitempty"`
	UsageSchema    map[string]jsplugin.UsageFieldSchema `json:"usage_schema,omitempty"`
}

type ModelPricingPluginVariant struct {
	PluginKey     string                               `json:"plugin_key"`
	PluginName    string                               `json:"plugin_name"`
	Icon          string                               `json:"icon,omitempty"`
	UsageSchema   map[string]jsplugin.UsageFieldSchema `json:"usage_schema"`
	UsageExamples []jsplugin.UsageExample              `json:"usage_examples,omitempty"`
	Configured    string                               `json:"configured"`
	Effective     string                               `json:"effective"`
	Compatible    bool                                 `json:"compatible"`
	Stale         bool                                 `json:"stale,omitempty"`
}

type ModelPricingSnapshot struct {
	Entries      []ModelPricingEntry `json:"entries"`
	Options      map[string]string   `json:"options"`
	EmptyVersion string              `json:"empty_version"`
}

var ErrModelPricingConflict = errors.New("model pricing changed; reload before saving")

// Lock order is stable across instances. Creating missing option rows inside
// the transaction also serializes the first write to an unconfigured database.
var modelPricingOptionKeys = []string{
	"AudioCompletionRatio", "AudioRatio", "CacheRatio", "CompletionRatio",
	"CreateCacheRatio", "ImageRatio", "ModelPrice", "ModelRatio",
	"billing_setting.billing_expr", "billing_setting.billing_mode", billing_setting.PluginBillingExprOption,
	billing_setting.GroupModelPricingOption,
}

var modelPricingMutationMu sync.Mutex

func IsModelPricingOption(key string) bool {
	return slices.Contains(modelPricingOptionKeys, key)
}

func ModelPricingVersion(values PricingValues) string {
	encoded, _ := common.Marshal(values)
	return fmt.Sprintf("%x", sha256.Sum256(encoded))
}

func defaultPricingMaps() map[string]map[string]any {
	result := make(map[string]map[string]any, len(modelPricingOptionKeys))
	for _, key := range modelPricingOptionKeys {
		result[key] = make(map[string]any)
	}
	for key, values := range ratio_setting.GetDefaultPricingMaps() {
		for name, value := range values {
			result[key][name] = value
		}
	}
	return result
}

func readModelPricingMaps(db *gorm.DB) (map[string]map[string]any, map[string]bool, []string, error) {
	var rows []Option
	if err := db.Where(commonKeyCol+" IN ?", modelPricingOptionKeys).Find(&rows).Error; err != nil {
		return nil, nil, nil, err
	}
	values := defaultPricingMaps()
	existing := make(map[string]bool)
	counts := make(map[string]int)
	for _, row := range rows {
		var entries map[string]any
		if err := common.UnmarshalJsonStr(row.Value, &entries); err != nil {
			return nil, nil, nil, fmt.Errorf("%s: %w", row.Key, err)
		}
		if entries == nil {
			return nil, nil, nil, fmt.Errorf("%s must be a JSON object", row.Key)
		}
		values[row.Key] = entries
		existing[row.Key] = true
		counts[row.Key]++
	}
	var duplicated []string
	for _, key := range modelPricingOptionKeys {
		if counts[key] > 1 {
			duplicated = append(duplicated, key)
		}
	}
	return values, existing, duplicated, nil
}

func modelPricingValues(values map[string]map[string]any, name string) PricingValues {
	result := make(PricingValues)
	for _, key := range modelPricingOptionKeys {
		if key == billing_setting.PluginBillingExprOption {
			variants := make(map[string]any)
			for variant, expression := range values[key] {
				if plugin, model, ok := billing_setting.SplitPluginBillingExprKey(variant); ok && model == name {
					variants[plugin] = expression
				}
			}
			if len(variants) > 0 {
				result[key] = variants
			}
			continue
		}
		if value, exists := values[key][name]; exists {
			result[key] = value
		}
	}
	return result
}

// replaceModelPricing writes one complete model draft into the option maps.
// Plugin expressions are grouped in the draft and flattened only in storage.
func replaceModelPricing(values map[string]map[string]any, name string, draft PricingValues) {
	for _, key := range modelPricingOptionKeys {
		if key == billing_setting.PluginBillingExprOption {
			for variant := range values[key] {
				if _, model, ok := billing_setting.SplitPluginBillingExprKey(variant); ok && model == name {
					delete(values[key], variant)
				}
			}
			variants, _ := draft[key].(map[string]any)
			for plugin, expr := range variants {
				values[key][billing_setting.PluginBillingExprKey(plugin, name)] = expr
			}
			continue
		}
		delete(values[key], name)
		if value, exists := draft[key]; exists {
			values[key][name] = value
		}
	}
}

func effectiveModelPricing(values map[string]map[string]any, name string) PricingValues {
	result := modelPricingValues(values, name)
	// Group overrides resolve separately per group; they are not part of the
	// model-level effective view.
	delete(result, billing_setting.GroupModelPricingOption)
	// Legacy wildcard aliases are resolved by the same normalization as relay.
	alias := ratio_setting.FormatMatchingModelName(name)
	for _, key := range []string{"ModelPrice", "ModelRatio", "CompletionRatio", "AudioRatio", "AudioCompletionRatio"} {
		delete(result, key)
		if value, exists := values[key][alias]; exists {
			result[key] = value
		}
	}
	mode, _ := result["billing_setting.billing_mode"].(string)
	if mode == "" {
		_, hasPrice := result["ModelPrice"]
		_, hasRatio := result["ModelRatio"]
		if _, builtin := billing_setting.GetBuiltinBillingExpr(name); builtin && !hasPrice && !hasRatio {
			mode = "tiered_expr"
		}
	}
	if mode == "tiered_expr" {
		result["billing_setting.billing_mode"] = mode
		if _, exists := result["billing_setting.billing_expr"]; !exists {
			if expression, ok := billing_setting.GetBuiltinBillingExpr(name); ok {
				result["billing_setting.billing_expr"] = expression
			}
		}
		return result
	}
	if _, exists := result["ModelPrice"]; exists {
		return result
	}
	if _, exists := result["ModelRatio"]; !exists && operation_setting.SelfUseModeEnabled {
		result["ModelRatio"] = float64(37.5)
	}
	// Completion ratios include engine-enforced model defaults. Expose their
	// effective value without persisting them into the editable configuration.
	var configuredCompletion *float64
	if ratio, exists := result["CompletionRatio"].(float64); exists {
		configuredCompletion = &ratio
	}
	result["CompletionRatio"] = ratio_setting.ResolveCompletionRatio(name, configuredCompletion).Ratio
	for key, fallback := range map[string]float64{
		"CacheRatio":       ratio_setting.DefaultCacheRatio,
		"CreateCacheRatio": ratio_setting.DefaultCreateCacheRatio,
		"ImageRatio":       ratio_setting.DefaultImageRatio,
	} {
		if _, exists := result[key]; !exists {
			result[key] = fallback
		}
	}
	return result
}

// effectiveGroupModelPricing overlays one group override on the model's
// effective pricing, mirroring the relay-time resolution in billing_setting:
// explicit mode wins, then field inference, then the global mode; a group
// ratio override suppresses a global fixed price.
func effectiveGroupModelPricing(name string, globalEffective PricingValues, entry map[string]any) PricingValues {
	result := maps.Clone(globalEffective)
	for key, value := range entry {
		result[key] = value
	}
	mode, _ := entry["billing_setting.billing_mode"].(string)
	if mode == "" {
		if _, hasExpr := entry["billing_setting.billing_expr"]; hasExpr {
			mode = billing_setting.BillingModeTieredExpr
		} else if _, hasPrice := entry["ModelPrice"]; hasPrice {
			mode = billing_setting.BillingModeRatio
		} else if _, hasRatio := entry["ModelRatio"]; hasRatio {
			mode = billing_setting.BillingModeRatio
		} else {
			mode, _ = globalEffective["billing_setting.billing_mode"].(string)
		}
	}
	if mode == billing_setting.BillingModeTieredExpr {
		result["billing_setting.billing_mode"] = billing_setting.BillingModeTieredExpr
		if _, hasExpr := entry["billing_setting.billing_expr"]; !hasExpr {
			if expression, ok := billing_setting.GetBillingExpr(name); ok {
				result["billing_setting.billing_expr"] = expression
			}
		}
		return result
	}
	result["billing_setting.billing_mode"] = billing_setting.BillingModeRatio
	delete(result, "billing_setting.billing_expr")
	if _, hasRatio := entry["ModelRatio"]; hasRatio {
		if _, hasPrice := entry["ModelPrice"]; !hasPrice {
			delete(result, "ModelPrice")
		}
	}
	return result
}

// PreviewModelPricing resolves a complete editable draft using the same defaults
// as the saved-price display and conversion. It has no write side effects.
func PreviewModelPricing(name string, draft PricingValues) (PricingValues, error) {
	if draft == nil {
		return nil, errors.New("pricing draft is required")
	}
	values, _, _, err := readModelPricingMaps(DB)
	if err != nil {
		return nil, err
	}
	if err := validateModelPricing(name, draft, modelPricingValues(values, name)); err != nil {
		return nil, err
	}
	replaceModelPricing(values, name, draft)
	return effectiveModelPricing(values, name), nil
}

func GetModelPricingSnapshot(names []string) (*ModelPricingSnapshot, error) {
	values, _, _, err := readModelPricingMaps(DB)
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		nameSet := make(map[string]bool)
		for key, entries := range values {
			for name := range entries {
				if key == billing_setting.PluginBillingExprOption {
					_, model, ok := billing_setting.SplitPluginBillingExprKey(name)
					if !ok {
						continue
					}
					name = model
				}
				nameSet[name] = true
			}
		}
		for name := range billing_setting.GetBuiltinBillingExprCopy() {
			nameSet[name] = true
		}
		for name := range nameSet {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	result := &ModelPricingSnapshot{Entries: make([]ModelPricingEntry, 0, len(names)), Options: make(map[string]string), EmptyVersion: ModelPricingVersion(PricingValues{})}
	generation := jsplugin.DefaultRegistry.Generation()
	for _, name := range names {
		configured := modelPricingValues(values, name)
		entry := ModelPricingEntry{ModelName: name, Version: ModelPricingVersion(configured), Configured: configured,
			ModelPricingDescription: ModelPricingDescription{Effective: effectiveModelPricing(values, name)}}
		if rawGroups, ok := configured[billing_setting.GroupModelPricingOption].(map[string]any); ok {
			entry.Groups = make(map[string]ModelPricingGroupEntry, len(rawGroups))
			for group, rawEntry := range rawGroups {
				groupValues, _ := rawEntry.(map[string]any)
				entry.Groups[group] = ModelPricingGroupEntry{
					Configured: PricingValues(groupValues),
					Effective:  effectiveGroupModelPricing(name, entry.Effective, groupValues),
				}
			}
		}
		entry.CacheWriteMode = ResolveCacheWriteMode(name, configured)
		entry.BillingDetails = ResolveLegacyBillingDetails(name, entry.Effective, configured)
		if plugin, ok := generation.GetByModel(name); ok {
			entry.UsageSchema, _ = plugin.Meta.UsageForModel(name)
		} else if target, ok := ResolveTaskModelAlias(generation, name); ok {
			if plugin, ok := generation.Get(target.PluginKey); ok {
				entry.UsageSchema, _ = plugin.Meta.UsageForModel(target.Declared)
			}
		}
		plugins := generation.PluginsByModel(name)
		configuredVariants, _ := configured[billing_setting.PluginBillingExprOption].(map[string]any)
		if len(plugins) >= 2 || len(configuredVariants) > 0 {
			keys := make(map[string]bool, len(plugins)+len(configuredVariants))
			for _, plugin := range plugins {
				keys[plugin.Meta.Key] = true
			}
			for key := range configuredVariants {
				keys[key] = true
			}
			for _, key := range slices.Sorted(maps.Keys(keys)) {
				configuredValue, overridden := configuredVariants[key]
				configuredExpr, _ := configuredValue.(string)
				plugin, exists := generation.Get(key)
				if !exists || !slices.Contains(plugin.Meta.Models, name) {
					variant := ModelPricingPluginVariant{
						PluginKey: key, PluginName: key, Configured: configuredExpr,
						UsageSchema: map[string]jsplugin.UsageFieldSchema{}, Stale: true,
					}
					if exists {
						variant.PluginName, variant.Icon = plugin.Meta.Name, plugin.Meta.Icon
					}
					entry.PluginVariants = append(entry.PluginVariants, variant)
					continue
				}
				schema, examples := plugin.Meta.UsageForModel(name)
				if schema == nil {
					schema = map[string]jsplugin.UsageFieldSchema{}
				}
				expression := configuredExpr
				if !overridden && entry.Effective["billing_setting.billing_mode"] == billing_setting.BillingModeTieredExpr {
					expression, _ = entry.Effective["billing_setting.billing_expr"].(string)
				}
				entry.PluginVariants = append(entry.PluginVariants, ModelPricingPluginVariant{
					PluginKey: plugin.Meta.Key, PluginName: plugin.Meta.Name, Icon: plugin.Meta.Icon,
					UsageSchema: schema, UsageExamples: examples, Configured: configuredExpr, Effective: expression,
					Compatible: billing_setting.TaskExprCompatible(expression, schema),
				})
			}
		}
		result.Entries = append(result.Entries, entry)
	}
	// Preserve the existing settings editor's full-map interface. Built-in
	// expressions are display defaults only; per-model writes do not persist them.
	for name, expression := range billing_setting.GetBuiltinBillingExprCopy() {
		effective := effectiveModelPricing(values, name)
		if effective["billing_setting.billing_mode"] != "tiered_expr" {
			continue
		}
		if _, ok := values["billing_setting.billing_mode"][name]; !ok {
			values["billing_setting.billing_mode"][name] = "tiered_expr"
		}
		if _, ok := values["billing_setting.billing_expr"][name]; !ok {
			values["billing_setting.billing_expr"][name] = expression
		}
	}
	for key, entries := range values {
		encoded, err := common.Marshal(entries)
		if err != nil {
			return nil, err
		}
		result.Options[key] = string(encoded)
	}
	return result, nil
}

func ValidateModelPricing(name string, values PricingValues) error {
	variants := make(map[string]any)
	for key, expression := range billing_setting.GetPluginBillingExprCopy() {
		if plugin, model, ok := billing_setting.SplitPluginBillingExprKey(key); ok && model == name {
			variants[plugin] = expression
		}
	}
	previous := PricingValues{billing_setting.PluginBillingExprOption: variants}
	if expression, ok := billing_setting.GetBillingExpr(name); ok {
		previous["billing_setting.billing_expr"] = expression
	}
	return validateModelPricing(name, values, previous)
}

// Writes pass the locked database snapshot here, so allowing an unchanged stale
// override cannot bypass validation through an out-of-date process-local cache.
func validateModelPricing(name string, values, previous PricingValues) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("model name is required")
	}
	generation := jsplugin.DefaultRegistry.Generation()
	previousVariants, _ := previous[billing_setting.PluginBillingExprOption].(map[string]any)
	variants := map[string]any{}
	if value, exists := values[billing_setting.PluginBillingExprOption]; exists {
		var ok bool
		variants, ok = value.(map[string]any)
		if !ok || variants == nil {
			return errors.New("plugin billing expressions must be a plugin-to-expression object")
		}
		for key, value := range variants {
			expression, ok := value.(string)
			if !ok || strings.TrimSpace(expression) == "" {
				return fmt.Errorf("model %s: plugin %s: billing expression is required", name, key)
			}
			plugin, exists := generation.Get(key)
			if !exists || !slices.Contains(plugin.Meta.Models, name) {
				if previousVariants[key] == expression {
					continue
				}
				return fmt.Errorf("model %s: plugin %s does not declare this model", name, key)
			}
			schema, _ := plugin.Meta.UsageForModel(name)
			if err := billing_setting.SmokeTestTaskExpr(expression, schema); err != nil {
				return fmt.Errorf("model %s: plugin %s: %w", name, key, err)
			}
		}
	}
	for key, value := range values {
		if key == billing_setting.PluginBillingExprOption || key == billing_setting.GroupModelPricingOption {
			continue
		}
		if !IsModelPricingOption(key) {
			return fmt.Errorf("unsupported pricing field: %s", key)
		}
		if key == "billing_setting.billing_mode" {
			if value != "ratio" && value != "tiered_expr" {
				return errors.New("invalid billing mode")
			}
			continue
		}
		if key == "billing_setting.billing_expr" {
			expression, ok := value.(string)
			if !ok || strings.TrimSpace(expression) == "" {
				return errors.New("billing expression is required")
			}
			previousExpression, _ := previous[key].(string)
			if err := smokeTestModelBillingExpr(name, expression, previousExpression, variants); err != nil {
				return err
			}
			continue
		}
		number, ok := value.(float64)
		if !ok || math.IsNaN(number) || math.IsInf(number, 0) || number < 0 {
			return fmt.Errorf("%s must be a finite, non-negative number", key)
		}
	}
	if values["billing_setting.billing_mode"] == "tiered_expr" {
		if _, exists := values["billing_setting.billing_expr"]; !exists {
			if _, builtin := billing_setting.GetBuiltinBillingExpr(name); !builtin {
				return errors.New("billing expression is required")
			}
		}
	}
	return validateGroupModelPricing(name, values, previous, variants)
}

// smokeTestModelBillingExpr compiles a billing expression and runs the
// schema-aware smoke tests. An unchanged stored usage expression whose plugin
// is gone has no schema to test; preserve it so removing stale overrides or
// saving other model prices does not become impossible.
func smokeTestModelBillingExpr(name, expression, previousExpression string, variants map[string]any) error {
	// Even a model expression currently shadowed by every provider must
	// compile; only its schema-specific smoke tests can be skipped.
	if _, err := billingexpr.CompileFromCache(expression); err != nil {
		return fmt.Errorf("model %s: %w", name, err)
	}
	generation := jsplugin.DefaultRegistry.Generation()
	var err error
	if plugins := generation.PluginsByModel(name); len(plugins) > 0 {
		for _, plugin := range plugins {
			if _, overridden := variants[plugin.Meta.Key]; overridden {
				continue
			}
			schema, _ := plugin.Meta.UsageForModel(name)
			if err = billing_setting.SmokeTestTaskExpr(expression, schema); err != nil {
				return fmt.Errorf("model %s: plugin %s: %w", name, plugin.Meta.Key, err)
			}
		}
	} else if target, resolved := ResolveTaskModelAlias(generation, name); resolved {
		if plugin, ok := generation.Get(target.PluginKey); ok {
			schema, _ := plugin.Meta.UsageForModel(target.Declared)
			err = billing_setting.SmokeTestTaskExpr(expression, schema)
		} else {
			err = billing_setting.SmokeTestExpr(expression)
		}
	} else if previousExpression != expression || len(billingexpr.UsedUsageKeys(expression)) == 0 {
		err = billing_setting.SmokeTestExpr(expression)
	}
	if err != nil {
		return fmt.Errorf("model %s: %w", name, err)
	}
	return nil
}

// groupPricingFieldKeys are the PricingValues keys a group override may set.
var groupPricingFieldKeys = []string{
	"billing_setting.billing_mode", "billing_setting.billing_expr",
	"ModelPrice", "ModelRatio", "CompletionRatio", "CacheRatio",
	"CreateCacheRatio", "ImageRatio", "AudioRatio", "AudioCompletionRatio",
}

// validateGroupModelPricing validates every group override in a model draft.
// Unchanged entries skip validation entirely, so stale overrides keep working
// when their group disappears or their task plugin is removed, mirroring the
// plugin-variant stale exemption.
func validateGroupModelPricing(name string, values, previous PricingValues, variants map[string]any) error {
	raw, exists := values[billing_setting.GroupModelPricingOption]
	if !exists {
		return nil
	}
	groups, ok := raw.(map[string]any)
	if !ok || groups == nil {
		return errors.New("group model pricing must be a group-to-pricing object")
	}
	previousGroups, _ := previous[billing_setting.GroupModelPricingOption].(map[string]any)
	for group, rawEntry := range groups {
		if strings.TrimSpace(group) == "" {
			return fmt.Errorf("model %s: group name is required", name)
		}
		entry, ok := rawEntry.(map[string]any)
		if !ok || len(entry) == 0 {
			return fmt.Errorf("model %s: group %s: pricing override must be a non-empty object", name, group)
		}
		var previousEntry map[string]any
		if previousGroups != nil {
			previousEntry, _ = previousGroups[group].(map[string]any)
		}
		if reflect.DeepEqual(previousEntry, entry) {
			continue
		}
		if err := validateGroupPricingEntry(name, group, entry, previousEntry, variants); err != nil {
			return err
		}
		if !groupPricingGroupExists(name, group) {
			return fmt.Errorf("model %s: group %s is not an available group for this model", name, group)
		}
	}
	return nil
}

func validateGroupPricingEntry(name, group string, entry, previousEntry map[string]any, variants map[string]any) error {
	for key, value := range entry {
		if !slices.Contains(groupPricingFieldKeys, key) {
			return fmt.Errorf("model %s: group %s: unsupported pricing field: %s", name, group, key)
		}
		switch key {
		case "billing_setting.billing_mode":
			if value != "ratio" && value != "tiered_expr" {
				return fmt.Errorf("model %s: group %s: invalid billing mode", name, group)
			}
		case "billing_setting.billing_expr":
			expression, ok := value.(string)
			if !ok || strings.TrimSpace(expression) == "" {
				return fmt.Errorf("model %s: group %s: billing expression is required", name, group)
			}
			// An unchanged stored usage expression whose plugin is gone has no
			// schema to test; preserve it like the model-level expression.
			previousExpression, _ := previousEntry["billing_setting.billing_expr"].(string)
			if err := smokeTestModelBillingExpr(name, expression, previousExpression, variants); err != nil {
				return fmt.Errorf("model %s: group %s: %w", name, group, err)
			}
		default:
			number, ok := value.(float64)
			if !ok || math.IsNaN(number) || math.IsInf(number, 0) || number < 0 {
				return fmt.Errorf("model %s: group %s: %s must be a finite, non-negative number", name, group, key)
			}
		}
	}
	// A group override must leave the group with a billable configuration:
	// its own fields or the model's global configuration must provide one.
	mode, _ := entry["billing_setting.billing_mode"].(string)
	_, hasExpr := entry["billing_setting.billing_expr"]
	_, hasPrice := entry["ModelPrice"]
	_, hasRatio := entry["ModelRatio"]
	if mode == "" {
		if hasExpr {
			mode = billing_setting.BillingModeTieredExpr
		} else if hasPrice || hasRatio {
			mode = billing_setting.BillingModeRatio
		}
	}
	if mode == billing_setting.BillingModeTieredExpr && !hasExpr {
		if _, fallback := billing_setting.GetBillingExpr(name); !fallback {
			return fmt.Errorf("model %s: group %s: tiered_expr billing requires an expression here or in the model pricing", name, group)
		}
	}
	if mode == billing_setting.BillingModeRatio && !hasPrice && !hasRatio {
		_, globalPrice := ratio_setting.GetModelPrice(name, false)
		if !globalPrice && !ratio_setting.HasConfiguredModelRatio(name) {
			return fmt.Errorf("model %s: group %s: ratio billing requires a price or ratio here or in the model pricing", name, group)
		}
	}
	return nil
}

// groupPricingGroupExists reports whether group can serve the model: it is
// enabled for the model through channel abilities or defined in group ratio
// settings (which lets administrators pre-configure pricing before a channel
// offers the model to that group).
func groupPricingGroupExists(name, group string) bool {
	if ratio_setting.ContainsGroupRatio(group) {
		return true
	}
	modelEnableGroupsLock.RLock()
	defer modelEnableGroupsLock.RUnlock()
	return slices.Contains(modelEnableGroups[name], group)
}

func UpdateModelPricing(changes []ModelPricingChange) error {
	if len(changes) == 0 {
		return errors.New("select model pricing changes before saving")
	}
	seen := make(map[string]bool)
	for _, change := range changes {
		if seen[change.ModelName] {
			return errors.New("duplicate model pricing change")
		}
		seen[change.ModelName] = true
		if change.ExpectedVersion == "" {
			return ErrModelPricingConflict
		}
	}
	return mutateModelPricingOptions(func(_ *gorm.DB, values map[string]map[string]any) error {
		defaults := defaultPricingMaps()
		for _, change := range changes {
			previous := modelPricingValues(values, change.ModelName)
			if ModelPricingVersion(previous) != change.ExpectedVersion {
				return fmt.Errorf("%w: %s", ErrModelPricingConflict, change.ModelName)
			}
			pricing := change.Pricing
			if change.Reset {
				pricing = modelPricingValues(defaults, change.ModelName)
			}
			if err := validateModelPricing(change.ModelName, pricing, previous); err != nil {
				return err
			}
			replaceModelPricing(values, change.ModelName, pricing)
		}
		return nil
	})
}

// UpdateModelPricingOptions keeps legacy single-option callers on the same
// locking, validation and transaction path as the model-level API.
func UpdateModelPricingOptions(updates map[string]string) error {
	return mutateModelPricingOptions(func(_ *gorm.DB, values map[string]map[string]any) error {
		previous := maps.Clone(values)
		names := make(map[string]bool)
		for key, raw := range updates {
			if !IsModelPricingOption(key) {
				return fmt.Errorf("unsupported pricing field: %s", key)
			}
			var entries map[string]any
			if err := common.UnmarshalJsonStr(raw, &entries); err != nil {
				return err
			}
			if entries == nil {
				return fmt.Errorf("%s must be a JSON object", key)
			}
			for _, entriesForKey := range []map[string]any{values[key], entries} {
				for name := range entriesForKey {
					if key == billing_setting.PluginBillingExprOption {
						_, model, ok := billing_setting.SplitPluginBillingExprKey(name)
						if !ok {
							return fmt.Errorf("invalid plugin billing expression key: %s", name)
						}
						name = model
					}
					names[name] = true
				}
			}
			values[key] = entries
		}
		for name := range names {
			if err := validateModelPricing(name, modelPricingValues(values, name), modelPricingValues(previous, name)); err != nil {
				return err
			}
		}
		return nil
	})
}

func mutateModelPricingOptions(mutate func(*gorm.DB, map[string]map[string]any) error) error {
	modelPricingMutationMu.Lock()
	defer modelPricingMutationMu.Unlock()
	var committed map[string]map[string]any
	err := DB.Transaction(func(tx *gorm.DB) error {
		values, existing, duplicated, err := readModelPricingMaps(lockForUpdate(tx))
		if err != nil {
			return err
		}
		if len(duplicated) > 0 {
			common.SysError("options table has duplicate pricing keys [" + strings.Join(duplicated, ", ") + "]; the table is missing a primary key")
		}
		defaults := defaultPricingMaps()
		for _, key := range modelPricingOptionKeys {
			if existing[key] {
				continue
			}
			encoded, err := common.Marshal(defaults[key])
			if err != nil {
				return err
			}
			row := Option{Key: key, Value: string(encoded)}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
				return err
			}
		}
		if err := mutate(tx, values); err != nil {
			return err
		}
		for _, key := range modelPricingOptionKeys {
			encoded, err := common.Marshal(values[key])
			if err != nil {
				return err
			}
			if err := tx.Model(&Option{}).Where(commonKeyCol+" = ?", key).Update("value", string(encoded)).Error; err != nil {
				return err
			}
		}
		committed = values
		return nil
	})
	if err != nil {
		return err
	}
	for _, key := range modelPricingOptionKeys {
		encoded, _ := common.Marshal(committed[key])
		if err := updateOptionMap(key, string(encoded)); err != nil {
			return err
		}
	}
	RefreshPricing()
	ratio_setting.InvalidateExposedDataCache()
	return nil
}
