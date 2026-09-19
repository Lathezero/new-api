package billing_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setGroupPricing(t *testing.T, value map[string]map[string]GroupPricing) {
	t.Helper()
	previous := billingSetting.GroupModelPricing
	billingSetting.GroupModelPricing = value
	t.Cleanup(func() { billingSetting.GroupModelPricing = previous })
}

func setBillingModeExpr(t *testing.T, modes, exprs map[string]string) {
	t.Helper()
	previousModes, previousExprs := billingSetting.BillingMode, billingSetting.BillingExpr
	billingSetting.BillingMode, billingSetting.BillingExpr = modes, exprs
	t.Cleanup(func() { billingSetting.BillingMode, billingSetting.BillingExpr = previousModes, previousExprs })
}

func float64Ptr(v float64) *float64 { return &v }

func TestGetGroupBillingMode(t *testing.T) {
	setGroupPricing(t, map[string]map[string]GroupPricing{
		"gp-mode-model": {
			"vip":     {Mode: BillingModeTieredExpr, Expr: `tier("base", p * 1)`},
			"default": {ModelRatio: float64Ptr(10)},
			"svip":    {Expr: `tier("base", p * 2)`},
			"custom":  {CompletionRatio: float64Ptr(3)},
		},
		"gp-global-tiered": {"vip": {ModelPrice: float64Ptr(1)}},
	})
	setBillingModeExpr(t, map[string]string{"gp-global-tiered": BillingModeTieredExpr}, map[string]string{"gp-global-tiered": `tier("base", p * 9)`})

	assert.Equal(t, BillingModeTieredExpr, GetGroupBillingMode("gp-mode-model", "vip"), "explicit mode wins")
	assert.Equal(t, BillingModeRatio, GetGroupBillingMode("gp-mode-model", "default"), "ratio field infers ratio mode")
	assert.Equal(t, BillingModeTieredExpr, GetGroupBillingMode("gp-mode-model", "svip"), "expr field infers tiered mode")
	assert.Equal(t, BillingModeRatio, GetGroupBillingMode("gp-mode-model", "custom"), "no pricing fields falls back to global default mode")
	assert.Equal(t, BillingModeRatio, GetGroupBillingMode("gp-mode-model", "unknown-group"), "group without override uses global mode")
	assert.Equal(t, BillingModeRatio, GetGroupBillingMode("gp-mode-model", ""), "empty group uses global mode")
	assert.Equal(t, BillingModeRatio, GetGroupBillingMode("gp-global-tiered", "vip"), "group price field overrides global tiered mode")
	assert.Equal(t, BillingModeTieredExpr, GetGroupBillingMode("gp-global-tiered", "default"), "group without override keeps global tiered mode")
}

func TestGetGroupModelPriceAndRatio(t *testing.T) {
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"gp-price-model": 2.5}`))
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"gp-ratio-model": 15}`))
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{}`))
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{}`))
	})
	setGroupPricing(t, map[string]map[string]GroupPricing{
		"gp-price-model": {
			"vip":  {ModelPrice: float64Ptr(0.5)},
			"svip": {ModelRatio: float64Ptr(20)},
		},
		"gp-ratio-model": {
			"vip": {ModelRatio: float64Ptr(30)},
		},
	})

	price, ok := GetGroupModelPrice("gp-price-model", "vip", false)
	assert.True(t, ok)
	assert.Equal(t, 0.5, price, "group price overrides global price")

	price, ok = GetGroupModelPrice("gp-price-model", "svip", false)
	assert.False(t, ok, "group ratio override suppresses the global fixed price")
	ratio, ok, _ := GetGroupModelRatio("gp-price-model", "svip")
	assert.True(t, ok)
	assert.Equal(t, 20.0, ratio)

	price, ok = GetGroupModelPrice("gp-price-model", "default", false)
	assert.True(t, ok)
	assert.Equal(t, 2.5, price, "group without override falls back to global price")

	ratio, ok, _ = GetGroupModelRatio("gp-ratio-model", "vip")
	assert.True(t, ok)
	assert.Equal(t, 30.0, ratio, "group ratio overrides global ratio")
	ratio, ok, _ = GetGroupModelRatio("gp-ratio-model", "default")
	assert.True(t, ok)
	assert.Equal(t, 15.0, ratio, "group without override falls back to global ratio")
}

func TestGetGroupBillingExpr(t *testing.T) {
	setGroupPricing(t, map[string]map[string]GroupPricing{
		"gp-expr-model": {"vip": {Expr: `tier("vip", p * 1)`}},
	})
	setBillingModeExpr(t, map[string]string{"gp-expr-model": BillingModeTieredExpr}, map[string]string{"gp-expr-model": `tier("base", p * 9)`})

	expr, ok := GetGroupBillingExpr("gp-expr-model", "vip")
	require.True(t, ok)
	assert.Equal(t, `tier("vip", p * 1)`, expr)
	expr, ok = GetGroupBillingExpr("gp-expr-model", "default")
	require.True(t, ok)
	assert.Equal(t, `tier("base", p * 9)`, expr, "group without override falls back to the model expression")
}

func TestGetGroupNumericRatios(t *testing.T) {
	require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(`{"gp-num-model": 2}`))
	require.NoError(t, ratio_setting.UpdateAudioRatioByJSONString(`{"gp-num-model": 4}`))
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(`{}`))
		require.NoError(t, ratio_setting.UpdateAudioRatioByJSONString(`{}`))
	})
	setGroupPricing(t, map[string]map[string]GroupPricing{
		"gp-num-model": {"vip": {CompletionRatio: float64Ptr(5)}},
	})

	assert.Equal(t, 5.0, GetGroupCompletionRatio("gp-num-model", "vip"))
	assert.Equal(t, 2.0, GetGroupCompletionRatio("gp-num-model", "default"))
	assert.Equal(t, 4.0, GetGroupAudioRatio("gp-num-model", "vip"), "unset audio ratio falls back to global")
	assert.Equal(t, 1.0, GetGroupAudioCompletionRatio("gp-num-model", "vip"), "unset audio completion ratio falls back to the global default")
}

func TestResolveTaskBillingExprForGroup(t *testing.T) {
	setGroupPricing(t, map[string]map[string]GroupPricing{
		"gp-task-model": {"vip": {Expr: `tier("vip", p * 1)`}},
	})
	setBillingModeExpr(t, map[string]string{"gp-task-model": BillingModeTieredExpr}, map[string]string{"gp-task-model": `tier("base", p * 9)`})
	previousPlugins := billingSetting.PluginBillingExpr
	billingSetting.PluginBillingExpr = map[string]string{"plugin-a::gp-task-model": `tier("plugin", p * 3)`}
	t.Cleanup(func() { billingSetting.PluginBillingExpr = previousPlugins })

	expr, ok := ResolveTaskBillingExprForGroup("plugin-a", "gp-task-model", "", "vip")
	require.True(t, ok)
	assert.Equal(t, `tier("plugin", p * 3)`, expr, "plugin override beats group override")

	expr, ok = ResolveTaskBillingExprForGroup("", "gp-task-model", "", "vip")
	require.True(t, ok)
	assert.Equal(t, `tier("vip", p * 1)`, expr, "group override beats model expression")

	expr, ok = ResolveTaskBillingExprForGroup("", "gp-task-model", "", "default")
	require.True(t, ok)
	assert.Equal(t, `tier("base", p * 9)`, expr, "group without override uses the model expression")
}

// A group override that switches the group to ratio billing must disable
// expression billing on the task path too, matching GetGroupBillingMode.
func TestResolveTaskBillingExprForGroupRatioOverrideDisablesExpr(t *testing.T) {
	setGroupPricing(t, map[string]map[string]GroupPricing{
		"gp-task-ratio": {"vip": {ModelRatio: float64Ptr(20)}},
	})
	setBillingModeExpr(t, map[string]string{"gp-task-ratio": BillingModeTieredExpr}, map[string]string{"gp-task-ratio": `tier("base", p * 9)`})
	previousPlugins := billingSetting.PluginBillingExpr
	billingSetting.PluginBillingExpr = map[string]string{"plugin-a::gp-task-ratio": `tier("plugin", p * 3)`}
	t.Cleanup(func() { billingSetting.PluginBillingExpr = previousPlugins })

	assert.Equal(t, BillingModeRatio, GetGroupBillingMode("gp-task-ratio", "vip"))
	_, ok := ResolveTaskBillingExprForGroup("", "gp-task-ratio", "", "vip")
	assert.False(t, ok, "ratio override suppresses the model expression")
	_, ok = ResolveTaskBillingExprForGroup("plugin-a", "gp-task-ratio", "", "vip")
	assert.False(t, ok, "ratio override suppresses the plugin override")

	expr, ok := ResolveTaskBillingExprForGroup("", "gp-task-ratio", "", "default")
	require.True(t, ok)
	assert.Equal(t, `tier("base", p * 9)`, expr, "groups without an override keep expression billing")
}
