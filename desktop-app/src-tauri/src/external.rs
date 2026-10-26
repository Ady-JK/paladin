// external.rs implements part of the Tauri backend.
// It supports the desktop app.

use tauri::WebviewBuilder;
use tauri::{AppHandle, LogicalPosition, LogicalSize, Manager, Runtime, WebviewUrl};

const PAGE_LABEL: &str = "external-page";

// desktop_user_agent is a fn in this module.
// It supports the desktop app backend.
fn desktop_user_agent() -> &'static str {
    if cfg!(target_os = "windows") {
        "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
    } else if cfg!(target_os = "macos") {
        "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
    } else {
        "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
    }
}

/// Shows the page, creating the webview the first time.
///
/// The rectangle is in logical pixels, relative to the main window: the
/// frontend reads it off the placeholder that the webview covers.
#[tauri::command]
pub async fn show_external_page<R: Runtime>(
    app: AppHandle<R>,
    url: String,
    x: f64,
    y: f64,
    width: f64,
    height: f64,
) -> Result<(), String> {
    let position = LogicalPosition::new(x, y);
    let size = LogicalSize::new(width, height);

    if let Some(view) = app.get_webview(PAGE_LABEL) {
        view.set_position(position).map_err(|e| e.to_string())?;
        view.set_size(size).map_err(|e| e.to_string())?;
        view.show().map_err(|e| e.to_string())?;
        return Ok(());
    }

    let window = app.get_window("main").ok_or("the main window is gone")?;
    let parsed: url::Url = url.parse().map_err(|e| format!("bad page url: {e}"))?;
    window
        .add_child(
            WebviewBuilder::new(PAGE_LABEL, WebviewUrl::External(parsed))
                .user_agent(desktop_user_agent()),
            position,
            size,
        )
        .map_err(|e| e.to_string())?;
    Ok(())
}

/// Loads another page in the same webview, keeping every login.
#[tauri::command]
pub async fn navigate_external_page<R: Runtime>(
    app: AppHandle<R>,
    url: String,
) -> Result<(), String> {
    let view = app.get_webview(PAGE_LABEL).ok_or("no such page")?;
    let parsed: url::Url = url.parse().map_err(|e| format!("bad page url: {e}"))?;
    view.navigate(parsed).map_err(|e| e.to_string())?;
    Ok(())
}

/// Moves the page to the rectangle of the placeholder.
#[tauri::command]
pub async fn move_external_page<R: Runtime>(
    app: AppHandle<R>,
    x: f64,
    y: f64,
    width: f64,
    height: f64,
) -> Result<(), String> {
    let view = app.get_webview(PAGE_LABEL).ok_or("no such page")?;
    view.set_position(LogicalPosition::new(x, y))
        .map_err(|e| e.to_string())?;
    view.set_size(LogicalSize::new(width, height))
        .map_err(|e| e.to_string())?;
    Ok(())
}

/// Reloads the page, for when it gets stuck.
#[tauri::command]
pub async fn reload_external_page<R: Runtime>(app: AppHandle<R>) -> Result<(), String> {
    if let Some(view) = app.get_webview(PAGE_LABEL) {
        view.reload().map_err(|e| e.to_string())?;
    }
    Ok(())
}

/// Hides the page. It is kept, so the logins survive.
#[tauri::command]
pub async fn hide_external_page<R: Runtime>(app: AppHandle<R>) -> Result<(), String> {
    if let Some(view) = app.get_webview(PAGE_LABEL) {
        view.hide().map_err(|e| e.to_string())?;
    }
    Ok(())
}
