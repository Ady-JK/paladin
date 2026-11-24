package claude

import "fmt"

// ForkBoundary resolves Paladin's user-message UUID to the last assistant
// entry of that completed turn. Claude's --resume-session-at takes that UUID,
// not the API message id and not the next user's prompt.
func ForkBoundary(sessionID, userID string) (string, error) {
	_, entries, err := ReadSession(sessionID)
	if err != nil {
		return "", err
	}
	found := false
	assistant := ""
	for _, r := range entries {
		if r.Str("type") == "user" && UserText(r.Obj("message")) != "" {
			if found {
				break
			}
			found = r.Str("uuid") == userID
		}
		if found && r.Str("type") == "assistant" {
			assistant = r.Str("uuid")
		}
	}
	if !found || assistant == "" {
		return "", fmt.Errorf("claude turn %s has no completed assistant checkpoint", userID)
	}
	return assistant, nil
}
