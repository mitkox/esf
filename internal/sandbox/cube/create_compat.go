package cube

import (
	"maps"
	"strings"

	cubesandbox "github.com/tencentcloud/CubeSandbox/sdk/go"
)

// CompatibleCreateOptions preserves explicit internet denial on CubeAPI 0.7.2 and 0.7.3.
// Those releases read the snake_case create field, while the published Go SDK
// sends camelCase. Network updates already use camelCase correctly. Limit the
// legacy field to the operator-recorded release so future aliasing servers do
// not receive duplicate names for the same serde field.
func CompatibleCreateOptions(opts cubesandbox.CreateOptions, version string) cubesandbox.CreateOptions {
	version = strings.TrimPrefix(version, "v")
	if (version != "0.7.2" && version != "0.7.3") || opts.AllowInternetAccess == nil || *opts.AllowInternetAccess {
		return opts
	}
	opts.Extra = maps.Clone(opts.Extra)
	if opts.Extra == nil {
		opts.Extra = make(map[string]any)
	}
	opts.Extra["allow_internet_access"] = false
	return opts
}
