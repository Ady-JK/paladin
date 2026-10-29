// dom.ts owns DOM access for the frontend.
// It holds the element lookups and the icon painter so feature modules
// share one set of references.

import { icon, type IconName } from "../icons";
import { state } from "./state";

export const $ = <T extends HTMLElement>(selector: string): T =>
  document.querySelector<T>(selector)!;

export const sidebarEl = $("#sidebar");
export const messagesEl = $("#messages");
export const chatViewEl = $("#chatView");
export const inputEl = $<HTMLTextAreaElement>("#promptInput");
// Tags a file or folder from the composer.
export const attachBtnEl = $<HTMLButtonElement>("#attachBtn");
export const sendBtn = $<HTMLButtonElement>("#sendBtn");
export const pauseBtn = $<HTMLButtonElement>("#pauseBtn");
// Resumes a turn paused at the round cap.
export const continueBtn = $<HTMLButtonElement>("#continueBtn");
export const statusDotEl = $("#daemonDot");
export const statusLabelEl = $("#daemonLabel");
export const statusDetailEl = $("#daemonDetail");
export const modelSelectBtn = $<HTMLButtonElement>("#modelSelectBtn");
export const modelSelectLabelEl = $("#modelSelectLabel");
export const modelMenu = $("#modelMenu");
export const modelSearchEl = $<HTMLInputElement>("#modelSearch");
export const modelSearchClearEl = $<HTMLButtonElement>("#modelSearchClear");
export const providerPickEl = $<HTMLSelectElement>("#providerPick");
export const labPickEl = $<HTMLSelectElement>("#labPick");
export const modelPillEl = $("#modelPill");
export const modelTreeEl = $("#modelTree");
export const modelDetailEl = $("#modelDetail");
export const modelBreadcrumbEl = $("#modelBreadcrumb");
export const modelQuickBtn = $<HTMLButtonElement>("#modelQuickBtn");
export const modelAdvancedBtn = $<HTMLButtonElement>("#modelAdvancedBtn");
export const filterBarEl = $("#filterBar");
export const filterLabelEl = $("#filterLabel");
export const filterClearBtn = $<HTMLButtonElement>("#filterClearBtn");
export const sessionModalEl = $("#sessionModal");
// Hosts the attach-a-file picker.
export const fileModalEl = $("#fileModal");
// Shows the picker directory.
export const filePathEl = $("#filePath");
// Lists the picker folders and files.
export const fileListEl = $("#fileList");
export const headerTitleEl = $("#headerTitle");
export const pageReloadBtnEl = $<HTMLButtonElement>("#pageReloadBtn");
export const workspaceWrapEl = $("#workspaceWrap");
export const statusWrapEl = $("#daemonWrap");
export const agentTabEl = $<HTMLButtonElement>("#agentTab");
export const chatTabEl = $<HTMLButtonElement>("#chatTab");
export const searchWrapEl = $("#searchWrap");
export const chatListEl = $("#chatList");
export const chatEntriesEl = $("#chatEntries");
export const newSessionBtnLabelEl = $("#newSessionBtnLabel");
export const siteModalEl = $("#siteModal");
export const siteNameEl = $<HTMLInputElement>("#siteName");
export const siteUrlEl = $<HTMLInputElement>("#siteUrl");
export const agentViewEl = $("#agentView");
export const externalViewEl = $("#externalView");
export const externalHostEl = $("#externalHost");
export const externalFallbackEl = $("#externalFallback");
export const externalFallbackLink = $<HTMLAnchorElement>("#externalFallbackLink");
export const newSessionPathEl = $("#newSessionPath");
export const newSessionListEl = $("#newSessionList");
export const sessionLabelEl = $("#sessionLabel");
// Shows the active session's context fill and spend; hidden when
// there is no session or no billed usage yet.
export const usagePillEl = $("#usagePill");
export const usageLabelEl = $("#usageLabel");
// Hosts the session analytics dashboard.
export const usageModalEl = $("#usageModal");
export const usageBodyEl = $("#usageBody");
export const usageModalSessionEl = $("#usageModalSession");
export const usageTabOverviewEl = $<HTMLButtonElement>("#usageTabOverview");
export const usageTabModelsEl = $<HTMLButtonElement>("#usageTabModels");
export const usageCloseBtnEl = $<HTMLButtonElement>("#usageCloseBtn");
export const sessionListEl = $("#sessionList");
export const workspaceLabelEl = $("#workspaceLabel");
export const settingsModalEl = $("#settingsModal");
export const settingsNavWorkspaceEl = $<HTMLButtonElement>("#settingsNavWorkspace");
export const settingsNavProvidersEl = $<HTMLButtonElement>("#settingsNavProviders");
export const settingsNavConnectionEl = $<HTMLButtonElement>("#settingsNavConnection");
// Opens the fleet-wide analytics pane inside settings.
export const settingsNavAnalyticsEl = $<HTMLButtonElement>("#settingsNavAnalytics");
// Opens the agent prompt editor inside settings.
export const settingsNavPromptEl = $<HTMLButtonElement>("#settingsNavPrompt");
export const settingsPaneTitleEl = $("#settingsPaneTitle");
export const settingsPaneWorkspaceEl = $("#settingsPaneWorkspace");
export const settingsPaneProvidersEl = $("#settingsPaneProviders");
// Hosts the agent prompt (SYSTEM_PROMPT.md / TOOL_GUIDANCE.md) editor.
export const settingsPanePromptEl = $("#settingsPanePrompt");
// Edits config/SYSTEM_PROMPT.md.
export const settingsSystemPromptEl = $<HTMLTextAreaElement>("#settingsSystemPrompt");
// Surfaces prompt save failures inline.
export const settingsPromptErrorEl = $("#settingsPromptError");
export const settingsPaneConnectionEl = $("#settingsPaneConnection");
// Hosts the fleet-wide analytics dashboard.
export const settingsPaneAnalyticsEl = $("#settingsPaneAnalytics");
// Hosts the rendered fleet-wide dashboard.
export const analyticsBodyEl = $("#analyticsBody");
// Appearance (motion level) and Resources (CPU/RAM) settings panes.
export const settingsNavAppearanceEl = $<HTMLButtonElement>("#settingsNavAppearance");
export const settingsPaneAppearanceEl = $("#settingsPaneAppearance");
export const settingsNavResourcesEl = $<HTMLButtonElement>("#settingsNavResources");
export const settingsPaneResourcesEl = $("#settingsPaneResources");
export const settingsNavMemoryEl = $<HTMLButtonElement>("#settingsNavMemory");
export const settingsPaneMemoryEl = $("#settingsPaneMemory");
export const settingsNavPermissionsEl = $<HTMLButtonElement>("#settingsNavPermissions");
export const settingsPanePermissionsEl = $("#settingsPanePermissions");
export const permBodyEl = $("#permBody");
export const memoryBodyEl = $("#memoryBody");
export const motionOptionsEl = $("#motionOptions");
export const settingsProviderListEl = $("#settingsProviderList");
export const settingsConnDotEl = $("#settingsConnDot");
export const settingsConnLabelEl = $("#settingsConnLabel");
export const settingsConnDetailEl = $("#settingsConnDetail");
export const providerCardEl = $("#providerCard");
export const providerCardTitleEl = $("#providerCardTitle");
export const providerCardNameEl = $<HTMLInputElement>("#providerCardName");
export const providerCardUrlEl = $<HTMLInputElement>("#providerCardUrl");
export const providerCardKeyEl = $<HTMLInputElement>("#providerCardKey");
export const providerCardTypeEl = $<HTMLSelectElement>("#providerCardType");
export const providerCardErrorEl = $("#providerCardError");
export const workspaceInputEl = $<HTMLInputElement>("#settingsDefaultWorkspace");
export const settingsWorkspaceBrowseBtn = $<HTMLButtonElement>("#settingsWorkspaceBrowseBtn");
export const workspaceBrowser = $("#settingsWorkspaceBrowser");
export const workspaceBrowserPath = $("#settingsWorkspaceBrowserPath");
export const workspaceBrowserList = $("#settingsWorkspaceBrowserList");
// Surfaces setWorkspace rejections (non-existent / non-directory paths)
// inline so a manual path entry failure is not toast-only.
export const settingsWorkspaceErrorEl = $("#settingsWorkspaceError");
export const searchInputEl = $<HTMLInputElement>("#searchInput");
export const confirmModalEl = $("#confirmModal");
export const confirmTitleEl = $("#confirmTitle");
export const toastEl = $("#toast");
export const toastTextEl = $("#toastText");
export const toastIconEl = $("#toastIcon");
// Hosts the attach picker breadcrumb trail.
export const fileCrumbsEl = $("#fileCrumbs");
// Filters the attach picker to matching names.
export const fileSearchEl = $<HTMLInputElement>("#fileSearch");
// Toggles dotfiles and dotfolders in the attach picker.
export const fileShowHiddenEl = $<HTMLInputElement>("#fileShowHidden");
// Confirms the highlighted file in the attach picker.
export const fileSelectFileBtn = $<HTMLButtonElement>("#fileSelectFileBtn");
// Confirms a folder from the attach picker.
export const fileSelectFolderBtn = $<HTMLButtonElement>("#fileSelectFolderBtn");
// Hosts the new-session breadcrumb trail.
export const newSessionCrumbsEl = $("#newSessionCrumbs");
// States which model the new session will use.
export const newSessionModelHintEl = $("#newSessionModelHint");
// Filters the new-session folder list.
export const newSessionFolderSearchEl = $<HTMLInputElement>("#newSessionFolderSearch");
// Toggles dotfolders in the new-session picker.
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
