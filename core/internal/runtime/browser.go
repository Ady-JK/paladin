package runtime

import "github.com/Ady-JK/paladin/core/internal/tools"

// SetBrowserSink connects model browser calls to the desktop's Playwright
// controller. CLI users without a desktop get a clear unavailable result.
func (r *AgentRuntime) SetBrowserSink(fn func(tools.BrowserRequest)) {
	if r.browser != nil {
		r.browser.SetSink(fn)
	}
}

func (r *AgentRuntime) BrowserReply(reply tools.BrowserReply) bool {
	return r.browser != nil && r.browser.Reply(reply)
}
