// dom.ts owns DOM access for the frontend.
// It holds the element lookups and the icon painter so feature modules
// share one set of references.

import { icon, type IconName } from "../icons";
import { state } from "./state";

export const $ = <T extends HTMLElement>(selector: string): T =>
  document.querySelector<T>(selector)!;

// sidebarEl is a const in this module.
// It supports the desktop app frontend.
export const sidebarEl = $("#sidebar");
// messagesEl is a const in this module.
// It supports the desktop app frontend.
export const messagesEl = $("#messages");
// chatViewEl is a const in this module.
// It supports the desktop app frontend.
export const chatViewEl = $("#chatView");
// inputEl is a const in this module.
// It supports the desktop app frontend.
export const inputEl = $<HTMLTextAreaElement>("#promptInput");
// attachBtnEl is a const in this module.
// It tags a file or folder from the composer.
export const attachBtnEl = $<HTMLButtonElement>("#attachBtn");
// sendBtn is a const in this module.
// It supports the desktop app frontend.
export const sendBtn = $<HTMLButtonElement>("#sendBtn");
// pauseBtn is a const in this module.
// It supports the desktop app frontend.
export const pauseBtn = $<HTMLButtonElement>("#pauseBtn");
// continueBtn is a const in this module.
// It resumes a turn paused at the round cap.
export const continueBtn = $<HTMLButtonElement>("#continueBtn");
// statusDotEl is a const in this module.
// It supports the desktop app frontend.
export const statusDotEl = $("#daemonDot");
// statusLabelEl is a const in this module.
// It supports the desktop app frontend.
export const statusLabelEl = $("#daemonLabel");
// statusDetailEl is a const in this module.
// It supports the desktop app frontend.
export const statusDetailEl = $("#daemonDetail");
// modelSelectBtn is a const in this module.
// It supports the desktop app frontend.
export const modelSelectBtn = $<HTMLButtonElement>("#modelSelectBtn");
// modelSelectLabelEl is a const in this module.
// It supports the desktop app frontend.
export const modelSelectLabelEl = $("#modelSelectLabel");
// modelMenu is a const in this module.
// It supports the desktop app frontend.
export const modelMenu = $("#modelMenu");
// modelSearchEl is a const in this module.
// It supports the desktop app frontend.
export const modelSearchEl = $<HTMLInputElement>("#modelSearch");
// modelSearchClearEl is a const in this module.
// It supports the desktop app frontend.
export const modelSearchClearEl = $<HTMLButtonElement>("#modelSearchClear");
// providerPickEl is a const in this module.
// It supports the desktop app frontend.
export const providerPickEl = $<HTMLSelectElement>("#providerPick");
// labPickEl is a const in this module.
// It supports the desktop app frontend.
export const labPickEl = $<HTMLSelectElement>("#labPick");
// modelPillEl is a const in this module.
// It supports the desktop app frontend.
export const modelPillEl = $("#modelPill");
// modelTreeEl is a const in this module.
// It supports the desktop app frontend.
export const modelTreeEl = $("#modelTree");
// modelDetailEl is a const in this module.
// It supports the desktop app frontend.
export const modelDetailEl = $("#modelDetail");
// modelBreadcrumbEl is a const in this module.
// It supports the desktop app frontend.
export const modelBreadcrumbEl = $("#modelBreadcrumb");
// modelQuickBtn is a const in this module.
// It supports the desktop app frontend.
export const modelQuickBtn = $<HTMLButtonElement>("#modelQuickBtn");
// modelAdvancedBtn is a const in this module.
// It supports the desktop app frontend.
export const modelAdvancedBtn = $<HTMLButtonElement>("#modelAdvancedBtn");
// filterBarEl is a const in this module.
// It supports the desktop app frontend.
export const filterBarEl = $("#filterBar");
// filterLabelEl is a const in this module.
// It supports the desktop app frontend.
export const filterLabelEl = $("#filterLabel");
// filterClearBtn is a const in this module.
// It supports the desktop app frontend.
export const filterClearBtn = $<HTMLButtonElement>("#filterClearBtn");
// sessionModalEl is a const in this module.
// It supports the desktop app frontend.
export const sessionModalEl = $("#sessionModal");
// fileModalEl is a const in this module.
// It hosts the attach-a-file picker.
export const fileModalEl = $("#fileModal");
// filePathEl is a const in this module.
// It shows the picker directory.
export const filePathEl = $("#filePath");
// fileListEl is a const in this module.
// It lists the picker folders and files.
export const fileListEl = $("#fileList");
// headerTitleEl is a const in this module.
// It supports the desktop app frontend.
export const headerTitleEl = $("#headerTitle");
// pageReloadBtnEl is a const in this module.
// It supports the desktop app frontend.
export const pageReloadBtnEl = $<HTMLButtonElement>("#pageReloadBtn");
// workspaceWrapEl is a const in this module.
// It supports the desktop app frontend.
export const workspaceWrapEl = $("#workspaceWrap");
// tabsWrapEl is a const in this module.
// It supports the desktop app frontend.
export const tabsWrapEl = $("#tabsWrap");
// daemonWrapEl is a const in this module.
// It supports the desktop app frontend.
export const daemonWrapEl = $("#daemonWrap");
// agentTabEl is a const in this module.
// It supports the desktop app frontend.
export const agentTabEl = $<HTMLButtonElement>("#agentTab");
// chatTabEl is a const in this module.
// It supports the desktop app frontend.
export const chatTabEl = $<HTMLButtonElement>("#chatTab");
// searchWrapEl is a const in this module.
// It supports the desktop app frontend.
export const searchWrapEl = $("#searchWrap");
// chatListEl is a const in this module.
// It supports the desktop app frontend.
export const chatListEl = $("#chatList");
// chatEntriesEl is a const in this module.
// It supports the desktop app frontend.
export const chatEntriesEl = $("#chatEntries");
// newSessionBtnLabelEl is a const in this module.
// It supports the desktop app frontend.
export const newSessionBtnLabelEl = $("#newSessionBtnLabel");
// siteModalEl is a const in this module.
// It supports the desktop app frontend.
export const siteModalEl = $("#siteModal");
// siteNameEl is a const in this module.
// It supports the desktop app frontend.
export const siteNameEl = $<HTMLInputElement>("#siteName");
// siteUrlEl is a const in this module.
// It supports the desktop app frontend.
export const siteUrlEl = $<HTMLInputElement>("#siteUrl");
// agentViewEl is a const in this module.
// It supports the desktop app frontend.
export const agentViewEl = $("#agentView");
// externalViewEl is a const in this module.
// It supports the desktop app frontend.
export const externalViewEl = $("#externalView");
// externalHostEl is a const in this module.
// It supports the desktop app frontend.
export const externalHostEl = $("#externalHost");
// externalFallbackEl is a const in this module.
// It supports the desktop app frontend.
export const externalFallbackEl = $("#externalFallback");
// externalFallbackLink is a const in this module.
// It supports the desktop app frontend.
export const externalFallbackLink = $<HTMLAnchorElement>("#externalFallbackLink");
// newSessionPathEl is a const in this module.
// It supports the desktop app frontend.
export const newSessionPathEl = $("#newSessionPath");
// newSessionListEl is a const in this module.
// It supports the desktop app frontend.
export const newSessionListEl = $("#newSessionList");
// sessionLabelEl is a const in this module.
// It supports the desktop app frontend.
export const sessionLabelEl = $("#sessionLabel");
// usagePillEl is a const in this module.
// It shows the active session's context fill and spend; hidden when
// there is no session or no billed usage yet.
export const usagePillEl = $("#usagePill");
// usageLabelEl is a const in this module.
// It supports the desktop app frontend.
export const usageLabelEl = $("#usageLabel");
// usageModalEl is a const in this module.
// It hosts the session analytics dashboard.
export const usageModalEl = $("#usageModal");
// usageBodyEl is a const in this module.
// It supports the desktop app frontend.
export const usageBodyEl = $("#usageBody");
// usageModalSessionEl is a const in this module.
// It supports the desktop app frontend.
export const usageModalSessionEl = $("#usageModalSession");
// usageTabOverviewEl is a const in this module.
// It supports the desktop app frontend.
export const usageTabOverviewEl = $<HTMLButtonElement>("#usageTabOverview");
// usageTabModelsEl is a const in this module.
// It supports the desktop app frontend.
export const usageTabModelsEl = $<HTMLButtonElement>("#usageTabModels");
// usageCloseBtnEl is a const in this module.
// It supports the desktop app frontend.
export const usageCloseBtnEl = $<HTMLButtonElement>("#usageCloseBtn");
// sessionListEl is a const in this module.
// It supports the desktop app frontend.
export const sessionListEl = $("#sessionList");
// workspaceLabelEl is a const in this module.
// It supports the desktop app frontend.
export const workspaceLabelEl = $("#workspaceLabel");
// settingsModalEl is a const in this module.
// It supports the desktop app frontend.
export const settingsModalEl = $("#settingsModal");
// settingsNavWorkspaceEl is a const in this module.
// It supports the desktop app frontend.
export const settingsNavWorkspaceEl = $<HTMLButtonElement>("#settingsNavWorkspace");
// settingsNavProvidersEl is a const in this module.
// It supports the desktop app frontend.
export const settingsNavProvidersEl = $<HTMLButtonElement>("#settingsNavProviders");
// settingsNavConnectionEl is a const in this module.
// It supports the desktop app frontend.
export const settingsNavConnectionEl = $<HTMLButtonElement>("#settingsNavConnection");
// settingsNavAnalyticsEl is a const in this module.
// It opens the fleet-wide analytics pane inside settings.
export const settingsNavAnalyticsEl = $<HTMLButtonElement>("#settingsNavAnalytics");
// settingsNavPromptEl is a const in this module.
// It opens the agent prompt editor inside settings.
export const settingsNavPromptEl = $<HTMLButtonElement>("#settingsNavPrompt");
// settingsPaneTitleEl is a const in this module.
// It supports the desktop app frontend.
export const settingsPaneTitleEl = $("#settingsPaneTitle");
// settingsPaneWorkspaceEl is a const in this module.
// It supports the desktop app frontend.
export const settingsPaneWorkspaceEl = $("#settingsPaneWorkspace");
// settingsPaneProvidersEl is a const in this module.
// It supports the desktop app frontend.
export const settingsPaneProvidersEl = $("#settingsPaneProviders");
// settingsPanePromptEl is a const in this module.
// It hosts the agent prompt (SYSTEM_PROMPT.md / TOOL_GUIDANCE.md) editor.
export const settingsPanePromptEl = $("#settingsPanePrompt");
// settingsSystemPromptEl is a const in this module.
// It edits config/SYSTEM_PROMPT.md.
export const settingsSystemPromptEl = $<HTMLTextAreaElement>("#settingsSystemPrompt");
// settingsPromptErrorEl is a const in this module.
// It surfaces prompt save failures inline.
export const settingsPromptErrorEl = $("#settingsPromptError");
// settingsPaneConnectionEl is a const in this module.
// It supports the desktop app frontend.
export const settingsPaneConnectionEl = $("#settingsPaneConnection");
// settingsPaneAnalyticsEl is a const in this module.
// It hosts the fleet-wide analytics dashboard.
export const settingsPaneAnalyticsEl = $("#settingsPaneAnalytics");
// analyticsBodyEl is a const in this module.
// It hosts the rendered fleet-wide dashboard.
export const analyticsBodyEl = $("#analyticsBody");
// Appearance (motion level) and Resources (CPU/RAM) settings panes.
export const settingsNavAppearanceEl = $<HTMLButtonElement>("#settingsNavAppearance");
export const settingsPaneAppearanceEl = $("#settingsPaneAppearance");
export const settingsNavResourcesEl = $<HTMLButtonElement>("#settingsNavResources");
export const settingsPaneResourcesEl = $("#settingsPaneResources");
export const motionOptionsEl = $("#motionOptions");
// settingsProviderListEl is a const in this module.
// It supports the desktop app frontend.
export const settingsProviderListEl = $("#settingsProviderList");
// settingsProviderHintEl is a const in this module.
// It supports the desktop app frontend.
export const settingsProviderHintEl = $("#settingsProviderHint");
// settingsConnDotEl is a const in this module.
// It supports the desktop app frontend.
export const settingsConnDotEl = $("#settingsConnDot");
// settingsConnLabelEl is a const in this module.
// It supports the desktop app frontend.
export const settingsConnLabelEl = $("#settingsConnLabel");
// settingsConnDetailEl is a const in this module.
// It supports the desktop app frontend.
export const settingsConnDetailEl = $("#settingsConnDetail");
// providerCardEl is a const in this module.
// It supports the desktop app frontend.
export const providerCardEl = $("#providerCard");
// providerCardTitleEl is a const in this module.
// It supports the desktop app frontend.
export const providerCardTitleEl = $("#providerCardTitle");
// providerCardNameEl is a const in this module.
// It supports the desktop app frontend.
export const providerCardNameEl = $<HTMLInputElement>("#providerCardName");
// providerCardUrlEl is a const in this module.
// It supports the desktop app frontend.
export const providerCardUrlEl = $<HTMLInputElement>("#providerCardUrl");
// providerCardKeyEl is a const in this module.
// It supports the desktop app frontend.
export const providerCardKeyEl = $<HTMLInputElement>("#providerCardKey");
// providerCardTypeEl is a const in this module.
// It supports the desktop app frontend.
export const providerCardTypeEl = $<HTMLSelectElement>("#providerCardType");
// providerCardErrorEl is a const in this module.
// It supports the desktop app frontend.
export const providerCardErrorEl = $("#providerCardError");
// remoteAddrEl is a const in this module.
// It supports the desktop app frontend.
export const remoteAddrEl = $<HTMLInputElement>("#remoteAddr");
// remoteTokenEl is a const in this module.
// It supports the desktop app frontend.
export const remoteTokenEl = $<HTMLInputElement>("#remoteToken");
// workspaceInputEl is a const in this module.
// It supports the desktop app frontend.
export const workspaceInputEl = $<HTMLInputElement>("#settingsDefaultWorkspace");
// settingsWorkspaceBrowseBtn is a const in this module.
// It supports the desktop app frontend.
export const settingsWorkspaceBrowseBtn = $<HTMLButtonElement>("#settingsWorkspaceBrowseBtn");
// workspaceBrowser is a const in this module.
// It supports the desktop app frontend.
export const workspaceBrowser = $("#settingsWorkspaceBrowser");
// workspaceBrowserPath is a const in this module.
// It supports the desktop app frontend.
export const workspaceBrowserPath = $("#settingsWorkspaceBrowserPath");
// workspaceBrowserList is a const in this module.
// It supports the desktop app frontend.
export const workspaceBrowserList = $("#settingsWorkspaceBrowserList");
// settingsWorkspaceErrorEl is a const in this module.
// It surfaces set_workspace rejections (non-existent / non-directory paths)
// inline so a manual path entry failure is not toast-only.
export const settingsWorkspaceErrorEl = $("#settingsWorkspaceError");
// searchInputEl is a const in this module.
// It supports the desktop app frontend.
export const searchInputEl = $<HTMLInputElement>("#searchInput");
// confirmModalEl is a const in this module.
// It supports the desktop app frontend.
export const confirmModalEl = $("#confirmModal");
// confirmTitleEl is a const in this module.
// It supports the desktop app frontend.
export const confirmTitleEl = $("#confirmTitle");
// toastEl is a const in this module.
// It supports the desktop app frontend.
export const toastEl = $("#toast");
// toastTextEl is a const in this module.
// It supports the desktop app frontend.
export const toastTextEl = $("#toastText");
// toastIconEl is a const in this module.
// It supports the desktop app frontend.
export const toastIconEl = $("#toastIcon");
// fileCrumbsEl is a const in this module.
// It hosts the attach picker breadcrumb trail.
export const fileCrumbsEl = $("#fileCrumbs");
// fileSearchEl is a const in this module.
// It filters the attach picker to matching names.
export const fileSearchEl = $<HTMLInputElement>("#fileSearch");
// fileShowHiddenEl is a const in this module.
// It toggles dotfiles and dotfolders in the attach picker.
export const fileShowHiddenEl = $<HTMLInputElement>("#fileShowHidden");
// fileSelectFileBtn is a const in this module.
// It confirms the highlighted file in the attach picker.
export const fileSelectFileBtn = $<HTMLButtonElement>("#fileSelectFileBtn");
// fileSelectFolderBtn is a const in this module.
// It confirms a folder from the attach picker.
export const fileSelectFolderBtn = $<HTMLButtonElement>("#fileSelectFolderBtn");
// newSessionFolderStepEl is a const in this module.
// It hosts the new-session folder browser pane.
export const newSessionFolderStepEl = $("#newSessionFolderStep");
// newSessionCrumbsEl is a const in this module.
// It hosts the new-session breadcrumb trail.
export const newSessionCrumbsEl = $("#newSessionCrumbs");
// newSessionModelHintEl is a const in this module.
// It states which model the new session will use.
export const newSessionModelHintEl = $("#newSessionModelHint");
// newSessionFolderSearchEl is a const in this module.
// It filters the new-session folder list.
export const newSessionFolderSearchEl = $<HTMLInputElement>("#newSessionFolderSearch");
// newSessionShowHiddenEl is a const in this module.
// It toggles dotfolders in the new-session picker.
export const newSessionShowHiddenEl = $<HTMLInputElement>("#newSessionShowHidden");

// paintIcons replaces [data-icon] placeholders with inline SVG.
// Pass the subtree that changed: a full-document sweep is O(n) and must
// only run once at startup. Hot paths pass their own container.
export function paintIcons(root: ParentNode = document): void {
  root.querySelectorAll<HTMLElement>("[data-icon]").forEach((holder) => {
    const name = holder.dataset.icon as IconName | undefined;
    if (!name) {
      return;
    }

    holder.innerHTML = icon(name);
  });

  const themeHolder = document.querySelector<HTMLElement>(
    "#themeToggleBtn [data-icon]",
  );
  if (themeHolder) {
    themeHolder.innerHTML = icon(state.dark ? "moon" : "sun");
  }
}
