pub const SYSTEM_PROMPT: &str = "You are Paladin, a personal coding partner working inside the user's workspace. Be direct and helpful. Prefer minimal, correct changes.";

pub const TOOL_GUIDANCE: &str = "Use tools to read and modify files. Prefer `read` before `edit`. Use `shell` for commands that need to run. Keep tool arguments exact.";

pub const USAGE_EXAMPLES: &str = "Paths: `workspace/<path>` is inside the workspace, otherwise use an absolute path. `read{path,limit?,offset?}`, `write{path,content,overwrite?}`, `edit{path,old_string,new_string,replace_all?}`, `shell{action:run|status|cancel,cmd?,workdir?,timeout_ms?,background?}`.";

pub fn assemble_system_prompt(
    base: &str,
    tool_guidance: &str,
    environment: &str,
    examples: &str,
    schemas: &str,
) -> String {
    let mut out = String::new();
    out.push_str(base);
    out.push_str("\n\n");
    out.push_str(tool_guidance);
    out.push_str("\n\nEnvironment:\n");
    out.push_str(environment);
    out.push_str("\n\n");
    out.push_str(examples);
    if !schemas.is_empty() {
        out.push_str("\n\n");
        out.push_str(schemas);
    }
    out
}

pub fn environment_block(os: &str, workspace: &str) -> String {
    format!("OS: {os}\nWorkspace: {workspace}")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn keeps_stable_prefix_order() {
        let prompt = assemble_system_prompt("base", "guide", "env", "examples", "schemas");
        let base_pos = prompt.find("base").unwrap();
        let guide_pos = prompt.find("guide").unwrap();
        let env_pos = prompt.find("env").unwrap();
        assert!(base_pos < guide_pos && guide_pos < env_pos);
    }
}
