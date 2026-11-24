package claude

import "context"

// Permission modes Paladin runs Claude Code in outside plan mode.
const (
	ModeBypass = "bypassPermissions"
	ModeAuto   = "auto"
	ModeManual = "default"
	ModePlan   = "plan"
)

// chooseActingMode picks the mode Claude runs in while it works. Paladin asks
// for bypass first. An organisation can disable it, and the CLI then starts in
// its default mode without failing, so current (the mode the CLI reports after
// initialize) is the signal. In that case auto mode is the closest substitute;
// if even that is refused the default mode stays and every request that reaches
// Paladin is still approved by its own permission callback.
func chooseActingMode(current string, setAuto func() error) string {
	if current == ModeBypass {
		return ModeBypass
	}
	if err := setAuto(); err != nil {
		return ModeManual
	}
	return ModeAuto
}

// ActingMode settles the working mode of a client started with ModeBypass.
func (c *Client) ActingMode(ctx context.Context) string {
	return chooseActingMode(c.Info.Str("current_permission_mode"), func() error {
		_, err := c.Call(ctx, map[string]any{"subtype": "set_permission_mode", "mode": ModeAuto})
		return err
	})
}
