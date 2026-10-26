// external.rs implements the embedded chat pages (Gemini, ChatGPT).
// The page must cover exactly the #externalHost placeholder, next to the
// sidebar, under the title bar.
//
// Resource design (Linux): exactly ONE page WebView ever, built on the MAIN
// window's own WebKit WebContext. That means one WebsiteDataStore (cookies,
// logins, disk cache shared), one NetworkProcess, and one shared secondary
// WebProcess for UI + page — never a second browser stack. The context runs
// CacheModel::DocumentViewer (smallest RAM cache) and
// ProcessModel::SharedSecondaryProcess. Hiding hides the PAGE widget itself
// (so WebKit throttles timers/rAF like a background tab) and mutes it;
// dropping destroys the widget (logins survive on disk) to reclaim the
// page's RAM outright.
//
// Why Linux needs its own backend: Tauri's `add_child` positions the child
// relative to the window's client area, but on Linux the child is packed
// into the window's vertical GtkBox with expand+fill, so the bounds are
// silently ignored. So the main layout is wrapped in a GtkOverlay, the page
// lives in an absolutely-positioned GtkFixed carrier, and geometry is plain
// margins + size request in widget pixels (= CSS pixels at any scale).
// Other platforms keep the Tauri child-webview path, where bounds work.

use tauri::{AppHandle, Manager, Runtime};

#[cfg(not(target_os = "linux"))]
use tauri::{LogicalPosition, LogicalSize, WebviewBuilder, WebviewUrl};

#[cfg(target_os = "linux")]
use gtk::glib::object::Cast;
#[cfg(target_os = "linux")]
use gtk::prelude::{ContainerExt, FixedExt, OverlayExt, WidgetExt};
#[cfg(target_os = "linux")]
#[allow(deprecated)]
use webkit2gtk::{CacheModel, HardwareAccelerationPolicy, ProcessModel, WebViewExt};

#[cfg(target_os = "linux")]
const OVERLAY_NAME: &str = "paladin-external-overlay";
#[cfg(target_os = "linux")]
const CARRIER_NAME: &str = "paladin-external-carrier";

#[cfg(not(target_os = "linux"))]
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

/// Round a CSS pixel value to whole widget pixels, clamped to >= 0.
#[cfg(target_os = "linux")]
fn px(value: f64) -> i32 {
    value.round() as i32
}

/// Compare URLs ignoring a single trailing slash, so re-showing the same
/// page never triggers a reload.
#[cfg(target_os = "linux")]
fn same_page(a: &str, b: &str) -> bool {
    fn norm(u: &str) -> &str {
        let u = u.trim_end_matches('/');
        if u.is_empty() {
            "/"
        } else {
            u
        }
    }
    norm(a) == norm(b)
}

#[cfg(target_os = "linux")]
fn run_on_main<R: Runtime, F, T>(app: &AppHandle<R>, f: F) -> Result<T, String>
where
    F: FnOnce() -> T + Send + 'static,
    T: Send + 'static,
{
    let (tx, rx) = std::sync::mpsc::channel();
    app.run_on_main_thread(move || {
        let _ = tx.send(f());
    })
    .map_err(|e| e.to_string())?;
    rx.recv().map_err(|e| format!("main thread dropped: {e}"))
}

#[cfg(target_os = "linux")]
fn find_overlay(gtk_window: &gtk::ApplicationWindow) -> Option<gtk::Overlay> {
    gtk_window.children().into_iter().find_map(|child| {
        if child.widget_name() == OVERLAY_NAME {
            child.downcast::<gtk::Overlay>().ok()
        } else {
            None
        }
    })
}

#[cfg(target_os = "linux")]
fn find_carrier(overlay: &gtk::Overlay) -> Option<gtk::Fixed> {
    overlay.children().into_iter().find_map(|child| {
        if child.widget_name() == CARRIER_NAME {
            child.downcast::<gtk::Fixed>().ok()
        } else {
            None
        }
    })
}

#[cfg(target_os = "linux")]
fn find_page(carrier: &gtk::Fixed) -> Option<webkit2gtk::WebView> {
    carrier
        .children()
        .into_iter()
        .find_map(|child| child.downcast::<webkit2gtk::WebView>().ok())
}

/// The main window's own WebKit context: sharing it is what keeps the app
/// to a single data store / network process / web process pool. The main
/// view is the first child of the window's default vbox.
#[cfg(target_os = "linux")]
fn main_web_context<R: Runtime>(app: &AppHandle<R>) -> Result<webkit2gtk::WebContext, String> {
    let window = app.get_window("main").ok_or("the main window is gone")?;
    let vbox = window.default_vbox().map_err(|e| e.to_string())?;
    let main_view = vbox
        .children()
        .into_iter()
        .find_map(|child| child.downcast::<webkit2gtk::WebView>().ok())
        .ok_or("the main view is gone")?;
    main_view
        .web_context()
        .ok_or_else(|| "the main view has no context".to_string())
}

/// Smallest RAM cache + one shared web process for every view on the
/// context. Idempotent: safe to call on every show.
#[cfg(target_os = "linux")]
#[allow(deprecated)]
fn tune_context(ctx: &webkit2gtk::WebContext) {
    use webkit2gtk::WebContextExt;
    ctx.set_cache_model(CacheModel::DocumentViewer);
    ctx.set_process_model(ProcessModel::SharedSecondaryProcess);
}

/// Wrap the main layout in a GtkOverlay (once) and return the overlay plus
/// the absolutely-positioned carrier for the page. Must run on the main
/// thread: every widget here is main-thread-only.
#[cfg(target_os = "linux")]
fn ensure_overlay<R: Runtime>(app: &AppHandle<R>) -> Result<(gtk::Overlay, gtk::Fixed), String> {
    let window = app.get_window("main").ok_or("the main window is gone")?;
    let gtk_window = window.gtk_window().map_err(|e| e.to_string())?;
    if let Some(overlay) = find_overlay(&gtk_window) {
        let carrier = find_carrier(&overlay).ok_or("the page carrier is gone")?;
        return Ok((overlay, carrier));
    }
    let vbox = window.default_vbox().map_err(|e| e.to_string())?;
    gtk_window.remove(&vbox);

    let overlay = gtk::Overlay::new();
    overlay.set_widget_name(OVERLAY_NAME);
    // The main layout stays the overlay's primary child, so it keeps
    // filling the window and resizing on its own.
    overlay.add(&vbox);

    // The carrier covers exactly the page rectangle: Start/Start alignment
    // with physical margins, forced LTR so locales never mirror it.
    let carrier = gtk::Fixed::new();
    carrier.set_widget_name(CARRIER_NAME);
    carrier.set_halign(gtk::Align::Start);
    carrier.set_valign(gtk::Align::Start);
    carrier.set_direction(gtk::TextDirection::Ltr);
    carrier.set_size_request(1, 1);
    overlay.add_overlay(&carrier);

    gtk_window.add(&overlay);
    // One tick: everything becomes visible except the still-empty carrier.
    overlay.show_all();
    carrier.hide();
    Ok((overlay, carrier))
}

#[cfg(target_os = "linux")]
fn place_carrier(carrier: &gtk::Fixed, x: f64, y: f64, width: f64, height: f64) {
    let x = px(x).max(0);
    let y = px(y).max(0);
    let width = px(width).max(1);
    let height = px(height).max(1);
    carrier.set_margin_start(x);
    carrier.set_margin_top(y);
    carrier.set_size_request(width, height);
    if let Some(page) = find_page(carrier) {
        page.set_size_request(width, height);
    }
}

/// Get the single page view, creating it on the shared context on first
/// use. Never navigates an existing view: callers decide via `uri()`.
#[cfg(target_os = "linux")]
fn ensure_page<R: Runtime>(
    app: &AppHandle<R>,
    carrier: &gtk::Fixed,
    width: f64,
    height: f64,
) -> Result<webkit2gtk::WebView, String> {
    if let Some(page) = find_page(carrier) {
        return Ok(page);
    }
    let ctx = main_web_context(app)?;
    tune_context(&ctx);
    // with_context shares the data store, network process, and process
    // pool with the main UI: no second browser stack, logins persist.
    let page = webkit2gtk::WebView::with_context(&ctx);
    if let Some(settings) = webkit2gtk::WebViewExt::settings(&page) {
        use webkit2gtk::SettingsExt;
        settings.set_user_agent(Some(desktop_user_agent()));
        // The GPU path is confirmed working on this machine: never leave
        // compositing to on-demand heuristics on the heavy pages.
        settings.set_hardware_acceleration_policy(HardwareAccelerationPolicy::Always);
        settings.set_enable_smooth_scrolling(true);
    }
    let w = px(width).max(1);
    let h = px(height).max(1);
    page.set_size_request(w, h);
    carrier.put(&page, 0, 0);
    page.show();
    Ok(page)
}

/// Shows the page, creating the webview the first time.
///
/// Same URL re-shown never reloads: toggling Agent/Chat is show/hide, not
/// a reboot. The rectangle is CSS pixels from the frontend's placeholder.
#[cfg(target_os = "linux")]
#[tauri::command]
pub async fn show_external_page<R: Runtime>(
    app: AppHandle<R>,
    url: String,
    x: f64,
    y: f64,
    width: f64,
    height: f64,
) -> Result<(), String> {
    let moved = app.clone();
    run_on_main(&app, move || {
        let (_overlay, carrier) = ensure_overlay(&moved)?;
        place_carrier(&carrier, x, y, width, height);
        let page = ensure_page(&moved, &carrier, width, height)?;
        let current = page.uri().map(|u| u.to_string()).unwrap_or_default();
        if !same_page(&current, &url) {
            page.load_uri(&url);
        }
        page.set_is_muted(false);
        page.show();
        carrier.show();
        Ok::<(), String>(())
    })?
}

/// Loads another page in the same webview, keeping every login.
/// Same URL is a no-op (no reboot on re-pick).
#[cfg(target_os = "linux")]
#[tauri::command]
pub async fn navigate_external_page<R: Runtime>(
    app: AppHandle<R>,
    url: String,
) -> Result<(), String> {
    let moved = app.clone();
    run_on_main(&app, move || {
        let (_overlay, carrier) = ensure_overlay(&moved)?;
        let page = find_page(&carrier).ok_or("no such page")?;
        let current = page.uri().map(|u| u.to_string()).unwrap_or_default();
        if !same_page(&current, &url) {
            page.load_uri(&url);
        }
        Ok::<(), String>(())
    })?
}

/// Moves the page to the rectangle of the placeholder.
#[cfg(target_os = "linux")]
#[tauri::command]
pub async fn move_external_page<R: Runtime>(
    app: AppHandle<R>,
    x: f64,
    y: f64,
    width: f64,
    height: f64,
) -> Result<(), String> {
    let moved = app.clone();
    run_on_main(&app, move || {
        let (_overlay, carrier) = ensure_overlay(&moved)?;
        if find_page(&carrier).is_none() {
            return Err("no such page".to_string());
        }
        place_carrier(&carrier, x, y, width, height);
        Ok::<(), String>(())
    })?
}

/// Reloads the page, for when it gets stuck.
#[cfg(target_os = "linux")]
#[tauri::command]
pub async fn reload_external_page<R: Runtime>(app: AppHandle<R>) -> Result<(), String> {
    let moved = app.clone();
    run_on_main(&app, move || {
        let (_overlay, carrier) = ensure_overlay(&moved)?;
        let page = find_page(&carrier).ok_or("no such page")?;
        page.reload();
        Ok::<(), String>(())
    })?
}

/// Hides the page and mutes it. The widget itself is hidden so WebKit
/// throttles its timers and animation frames like a background tab; the
/// view is kept so showing again is instant.
#[cfg(target_os = "linux")]
#[tauri::command]
pub async fn hide_external_page<R: Runtime>(app: AppHandle<R>) -> Result<(), String> {
    let moved = app.clone();
    run_on_main(&app, move || {
        let (_overlay, carrier) = ensure_overlay(&moved)?;
        if let Some(page) = find_page(&carrier) {
            page.set_is_muted(true);
            page.hide();
        }
        carrier.hide();
        Ok::<(), String>(())
    })?
}

/// Destroys the hidden page to reclaim its RAM outright (JS heap, decoded
/// images, GPU buffers). Cookies, disk cache, and logins live in the shared
/// data store, so the next show signs straight back in. No-op when visible.
#[cfg(target_os = "linux")]
#[tauri::command]
pub async fn drop_external_page<R: Runtime>(app: AppHandle<R>) -> Result<(), String> {
    let moved = app.clone();
    run_on_main(&app, move || {
        let (_overlay, carrier) = ensure_overlay(&moved)?;
        if carrier.is_visible() {
            return Ok::<(), String>(());
        }
        if let Some(page) = find_page(&carrier) {
            page.stop_loading();
            carrier.remove(&page);
            // Dropping our last reference here destroys the widget and
            // frees the page's JS heap, decoded images, and GPU buffers.
            drop(page);
        }
        Ok::<(), String>(())
    })?
}

#[cfg(target_os = "linux")]
#[cfg(test)]
mod tests {
    use super::{px, same_page};

    #[test]
    fn rounds_css_pixels_to_whole_widgets() {
        assert_eq!(px(288.0), 288);
        assert_eq!(px(43.5), 44);
        assert_eq!(px(43.4), 43);
        assert_eq!(px(0.0), 0);
    }

    #[test]
    fn same_url_never_reloads() {
        assert!(same_page(
            "https://gemini.google.com/",
            "https://gemini.google.com"
        ));
        assert!(same_page(
            "https://gemini.google.com",
            "https://gemini.google.com/"
        ));
        assert!(!same_page(
            "https://gemini.google.com/",
            "https://chatgpt.com/"
        ));
        assert!(!same_page("", "https://gemini.google.com/"));
    }
}

/// Shows the page, creating the webview the first time.
///
/// The rectangle is in logical pixels, relative to the main window: the
/// frontend reads it off the placeholder that the webview covers.
#[cfg(not(target_os = "linux"))]
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
#[cfg(not(target_os = "linux"))]
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
#[cfg(not(target_os = "linux"))]
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
#[cfg(not(target_os = "linux"))]
#[tauri::command]
pub async fn reload_external_page<R: Runtime>(app: AppHandle<R>) -> Result<(), String> {
    if let Some(view) = app.get_webview(PAGE_LABEL) {
        view.reload().map_err(|e| e.to_string())?;
    }
    Ok(())
}

/// Hides the page. It is kept, so the logins survive.
#[cfg(not(target_os = "linux"))]
#[tauri::command]
pub async fn hide_external_page<R: Runtime>(app: AppHandle<R>) -> Result<(), String> {
    if let Some(view) = app.get_webview(PAGE_LABEL) {
        view.hide().map_err(|e| e.to_string())?;
    }
    Ok(())
}

/// Frees the hidden page. Non-Linux keeps the child webview (cheap there).
#[cfg(not(target_os = "linux"))]
#[tauri::command]
pub async fn drop_external_page<R: Runtime>(_app: AppHandle<R>) -> Result<(), String> {
    Ok(())
}
