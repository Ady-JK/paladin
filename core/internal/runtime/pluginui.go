// pluginui.go serves the side-pane panels and tool-result cards the
// enabled extension plugins ship. It reads manifests only: no plugin
// process starts just to open the Plugins menu.

package runtime

import "github.com/Ady-JK/paladin/core/internal/extension"

// PluginViews reports every validated plugin panel and card, plus the
// UI entries that were skipped. It is safe to call before the first
// turn: nothing is cached and no runner starts.
func (r *AgentRuntime) PluginViews() extension.PluginUI {
	return r.extensions.UI()
}
