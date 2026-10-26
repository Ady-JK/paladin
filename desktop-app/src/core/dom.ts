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
// newSessionSearchEl is a const in this module.
// It supports the desktop app frontend.
export const newSessionSearchEl = $<HTMLInputElement>("#newSessionSearch");
// newSessionTreeEl is a const in this module.
// It supports the desktop app frontend.
export const newSessionTreeEl = $("#newSessionTree");
// newSessionCrumbEl is a const in this module.
// It supports the desktop app frontend.
export const newSessionCrumbEl = $("#newSessionCrumb");
// filterBarEl is a const in this module.
// It supports the desktop app frontend.
export const filterBarEl = $("#filterBar");
// filterLabelEl is a const in this module.
// It supports the desktop app frontend.
export const filterLabelEl = $("#filterLabel");
// filterClearBtn is a const in this module.
// It supports the desktop app frontend.
export const filterClearBtn = $<HTMLButtonElement>("#filterClearBtn");
// newSessionFilterBarEl is a const in this module.
// It supports the desktop app frontend.
export const newSessionFilterBarEl = $("#newSessionFilterBar");
// newSessionFilterLabelEl is a const in this module.
// It supports the desktop app frontend.
export const newSessionFilterLabelEl = $("#newSessionFilterLabel");
// newSessionFilterClearBtn is a const in this module.
// It supports the desktop app frontend.
export const newSessionFilterClearBtn = $<HTMLButtonElement>("#newSessionFilterClearBtn");
// sessionModalEl is a const in this module.
// It supports the desktop app frontend.
export const sessionModalEl = $("#sessionModal");
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
// settingsPaneTitleEl is a const in this module.
// It supports the desktop app frontend.
export const settingsPaneTitleEl = $("#settingsPaneTitle");
// settingsPaneWorkspaceEl is a const in this module.
// It supports the desktop app frontend.
export const settingsPaneWorkspaceEl = $("#settingsPaneWorkspace");
// settingsPaneProvidersEl is a const in this module.
// It supports the desktop app frontend.
export const settingsPaneProvidersEl = $("#settingsPaneProviders");
// settingsPaneConnectionEl is a const in this module.
// It supports the desktop app frontend.
export const settingsPaneConnectionEl = $("#settingsPaneConnection");
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

// paintIcons is a function in this module.
// It supports the desktop app frontend.
export function paintIcons(): void {
  document.querySelectorAll<HTMLElement>("[data-icon]").forEach((holder) => {
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
