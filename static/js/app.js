(function () {
  const S = window.CloudClipboardState;
  const api = window.CloudClipboardApi;
  const realtime = window.CloudClipboardRealtime;
  const app = document.getElementById('app');
  const state = S.createInitialState();

  let ws = null;
  let pollTimer = null;
  let reconnectTimer = null;
  let lifecycleTimer = null;
  let rendered = false;
  let bannerTimeout = null;
  let activeEnterToken = 0;
  let suppressNextHashChange = false;
  let globalEventsBound = false;
  let sendInFlight = false;
  let uploadInFlight = false;
  let messageActionInFlight = new Set();
  
  const RECENT_ROOMS_STORAGE_KEY = 'cloudClipboardRecentRooms';
  const MAX_RECENT_ROOMS = 5;
  const DURATION_SETTING_KEYS = new Set([
    'roomDefaultTTLSec',
    'roomExtendSec',
    'roomCanExtendSec',
    'fileMessageExpireSec',
  ]);
  const BYTE_SETTING_KEYS = new Set(['maxUploadBytes']);
  const BOOL_SETTING_KEYS = new Set(['fileNeverExpire']);
  const THEME_STORAGE_KEY = 'cloudClipboardTheme';
  const ACCENT_STORAGE_KEY = 'cloudClipboardAccent';
  const ACCENT_PRESETS = [
    { id: 'sky', color: '#0ea5e9', name: '天蓝' },
    { id: 'emerald', color: '#10b981', name: '翠绿' },
    { id: 'amber', color: '#f59e0b', name: '琥珀' },
    { id: 'rose', color: '#f43f5e', name: '玫红' },
    { id: 'indigo', color: '#6366f1', name: '靛青' },
  ];

  window.addEventListener('hashchange', () => {
    if (suppressNextHashChange) {
      suppressNextHashChange = false;
      return;
    }

    const hash = window.location.hash;
    const newCode = hash.startsWith('#') ? hash.slice(1).trim().toUpperCase() : '';
    if (newCode && newCode !== state.roomCode) {
      enterRoom(newCode, { syncHash: false });
    } else if (!newCode && state.currentRoom && !state.workspaceTransition) {
      leaveCurrentRoom();
      render();
    }
  });

  function esc(v) { return S.escapeHTML(v); }
  function fmt(v) { return S.formatDate(v); }
  function formatSize(bytes) {
    if (!bytes) return '';
    if (bytes < 1024) return bytes + ' B';
    if (bytes < 1048576) return (bytes / 1024).toFixed(1) + ' KB';
    if (bytes < 1073741824) return (bytes / 1048576).toFixed(1) + ' MB';
    return (bytes / 1073741824).toFixed(1) + ' GB';
  }

  // 简短格式：小于 500M / 小于 1.2G
  function formatSizeForLimit(bytes) {
    if (!bytes) return '';
    const mb = bytes / 1048576;
    if (mb < 1024) {
      if (mb < 1) return `小于 ${(bytes / 1024).toFixed(0)}KB`;
      return `小于 ${mb.toFixed(0)}MB`;
    }
    return `小于 ${(mb / 1024).toFixed(1)}G`;
  }

  function formatSpeed(bytesPerSecond) {
    if (!bytesPerSecond) return '';
    return `${formatSize(bytesPerSecond)}/s`;
  }

  function isImageMessage(msg) {
    return msg?.type === 'file' && typeof msg.mimeType === 'string' && msg.mimeType.startsWith('image/');
  }

  function isImageFile(file) {
    return !!file && typeof file.type === 'string' && file.type.startsWith('image/');
  }

  function isMessageExpanded(msgId) {
    return !!state.expandedMessageIds[msgId];
  }

  function toggleMessageExpanded(msgId) {
    state.expandedMessageIds[msgId] = !state.expandedMessageIds[msgId];
    const card = document.getElementById(`msg-${msgId}`);
    if (card) {
      const expanded = state.expandedMessageIds[msgId];
      card.classList.toggle('expanded', expanded);
      const body = card.querySelector('.message-body');
      const img = body?.querySelector('.image-message');
      const pre = body?.querySelector('.message-text');
      const hint = card.querySelector('.message-expand-hint');
      if (img) { img.classList.toggle('expanded', expanded); img.classList.toggle('collapsed', !expanded); }
      if (pre) { pre.classList.toggle('expanded', expanded); pre.classList.toggle('collapsed', !expanded); }
      if (hint) { hint.textContent = expanded ? '点击收起' : '点击展开'; }
    }
  }

  // 检测文本中的 URL，返回带链接的 HTML（已转义非 URL 部分）
  function linkifyText(text) {
    const urlRegex = /https?:\/\/[^\s<>"{}|\\^`[\]]+/gi;
    const parts = [];
    let lastIndex = 0;
    let match;
    while ((match = urlRegex.exec(text)) !== null) {
      if (match.index > lastIndex) {
        parts.push(esc(text.slice(lastIndex, match.index)));
      }
      const url = match[0];
      parts.push(`<a href="${esc(url)}" target="_blank" rel="noopener noreferrer" class="msg-link">${esc(url)}</a>`);
      lastIndex = urlRegex.lastIndex;
    }
    if (lastIndex < text.length) {
      parts.push(esc(text.slice(lastIndex)));
    }
    return parts.join('');
  }

  function renderIcon(name, alt, className = 'app-icon') {
    return `<img src="/icon/${name}.svg" alt="${esc(alt || '')}" class="${className}" aria-hidden="true" />`;
  }

  function setConnectionStatus(s) {
    if (state.connectionStatus === s) return;
    state.connectionStatus = s;
    state.connectionLabel = S.statusLabel(s);
    updateConnectionStatus();
  }

  function banner(type, msg) {
    if (bannerTimeout) clearTimeout(bannerTimeout);
    if (type === 'error') { state.error = msg; state.success = ''; state.notice = ''; }
    else if (type === 'success') { state.success = msg; state.error = ''; state.notice = ''; }
    else if (type === 'notice') { state.notice = msg; state.error = ''; state.success = ''; }
    else { state.error = ''; state.success = ''; state.notice = ''; }
    updateBanners();
    if (msg) {
      bannerTimeout = setTimeout(() => {
        // 先加退出动画
        const cards = document.querySelectorAll('#banners .toast-card');
        cards.forEach(c => c.classList.add('toast-out'));
        setTimeout(() => {
          state.error = '';
          state.success = '';
          state.notice = '';
          updateBanners();
        }, 250);
      }, 2500);
    }
  }

  function sortMessages(messages) {
    return [...messages].sort((a, b) => {
      const pinDiff = Number(Boolean(b.isPinned)) - Number(Boolean(a.isPinned));
      if (pinDiff !== 0) return pinDiff;
      const createdDiff = Number(b.createdAt || 0) - Number(a.createdAt || 0);
      if (createdDiff !== 0) return createdDiff;
      return Number(b.id || 0) - Number(a.id || 0);
    });
  }

  function setHash(roomCode) {
    const normalized = (roomCode || '').trim().toUpperCase();
    const nextHash = normalized ? `#${normalized}` : '';
    if (window.location.hash === nextHash) return;
    suppressNextHashChange = true;
    window.location.hash = nextHash;
  }

  function leaveCurrentRoom() {
    disconnectRealtime();
    state.currentRoom = null;
    state.messages = [];
    state.roomCode = '';
    state.filesToUpload = [];
    state.lastEventId = 0;
    state.draft = '';
    state.mobileQRVisible = false;
    state.mobileRoomCardVisible = false;
    state.workspaceTransition = '';
    state.showScrollTop = false;
  }

  function openAdminView() {
    state.activeView = 'admin';
    window.scrollTo({ top: 0, behavior: 'instant' });
    updateViewState();
    updateAdminView();
    if (state.authenticated) {
      loadAdminRooms();
      if (!state.adminSettings) loadAdminSettings();
    }
  }

  function closeAdminView() {
    state.activeView = 'workspace';
    updateViewState();
    updateAdminView();
  }

  function leaveRoomFromTopbar() {
    if (!state.currentRoom) return;
    leaveCurrentRoom();
    setHash('');
    render();
    banner('notice', '已退出当前房间');
  }

  async function copyToClipboard(text, successMessage) {
    try {
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(text);
      } else {
        const textarea = document.createElement('textarea');
        textarea.value = text;
        textarea.setAttribute('readonly', 'readonly');
        textarea.style.position = 'fixed';
        textarea.style.opacity = '0';
        document.body.appendChild(textarea);
        textarea.select();
        const ok = document.execCommand('copy');
        document.body.removeChild(textarea);
        if (!ok) throw new Error('copy failed');
      }
      banner('success', successMessage);
      return true;
    } catch (_) {
      banner('error', '复制失败，请手动复制');
      return false;
    }
  }

  function getRoomUrl(roomCode) {
    return `${state.origin}/#${roomCode}`;
  }

  function secondsToHours(value) {
    if (value === undefined || value === null || Number.isNaN(Number(value))) return '';
    return Number(value) / 3600;
  }

  function hoursToSeconds(value) {
    if (value === undefined || value === null || value === '') return 0;
    return Math.round(Number(value) * 3600);
  }

  function bytesToMegabytes(value) {
    if (value === undefined || value === null || Number.isNaN(Number(value))) return '';
    return Math.round(Number(value) / (1024 * 1024));
  }

  function megabytesToBytes(value) {
    if (value === undefined || value === null || value === '') return 0;
    return Math.round(Number(value) * 1024 * 1024);
  }

  function normalizeAdminSettings(settings) {
    const next = { ...(settings || {}) };
    DURATION_SETTING_KEYS.forEach(key => {
      if (next[key] !== undefined) next[key] = secondsToHours(next[key]);
    });
    BYTE_SETTING_KEYS.forEach(key => {
      if (next[key] !== undefined) next[key] = bytesToMegabytes(next[key]);
    });
    return next;
  }

  function denormalizeAdminSettings(settings) {
    const next = { ...(settings || {}) };
    DURATION_SETTING_KEYS.forEach(key => {
      if (next[key] !== undefined) next[key] = hoursToSeconds(next[key]);
    });
    BYTE_SETTING_KEYS.forEach(key => {
      if (next[key] !== undefined) next[key] = megabytesToBytes(next[key]);
    });
    return next;
  }

  function getEmptyStateTitle() {
    return state.publicSettings?.emptyStateTitle || '选择一个房间开始';
  }

  function getEmptyStateBody() {
    return state.publicSettings?.emptyStateBody || '•尝试创建新房间或通过房间号加入已有房间，即可开始同步内容\n•您的数据将在服务器上有限期存储，除非您手动设置为永久房间\n•本服务仅作为数据传输工具，请勿依赖本服务作为唯一存储介质\n•房间隔离码不足以构成加密保护，请勿传输未加密重要敏感信息\n•您需对传输内容的合法性自行承担全部责任，禁止传播非法内容';
  }

  function getRoomDisplayName(room) {
    if (!room) return '';
    return room.roomName?.trim() || room.roomCode;
  }

  function loadRecentRooms() {
    try {
      const raw = window.localStorage.getItem(RECENT_ROOMS_STORAGE_KEY);
      if (!raw) return [];
      const parsed = JSON.parse(raw);
      return Array.isArray(parsed) ? parsed.slice(0, MAX_RECENT_ROOMS) : [];
    } catch (_) {
      return [];
    }
  }

  function saveRecentRooms(rooms) {
    state.recentRooms = rooms.slice(0, MAX_RECENT_ROOMS);
    try {
      window.localStorage.setItem(RECENT_ROOMS_STORAGE_KEY, JSON.stringify(state.recentRooms));
    } catch (_) {
      // ignore storage failures
    }
  }

  function rememberRecentRoom(room) {
    if (!room?.roomCode) return;
    const next = [
      {
        roomCode: room.roomCode,
        roomName: room.roomName || '',
        lastVisitedAt: Math.floor(Date.now() / 1000),
      },
      ...state.recentRooms.filter(item => item.roomCode !== room.roomCode),
    ];
    saveRecentRooms(next);
  }

  function removeRecentRoom(roomCode) {
    if (!roomCode) return;
    saveRecentRooms(state.recentRooms.filter(item => item.roomCode !== roomCode));
  }

  function loadThemePreference() {
    try {
      return window.localStorage.getItem(THEME_STORAGE_KEY) || 'light';
    } catch (_) {
      return 'light';
    }
  }

  function applyTheme(theme) {
    state.theme = theme === 'light' ? 'light' : 'dark';
    document.documentElement.dataset.theme = state.theme;
  }

  function saveThemePreference(theme) {
    try {
      window.localStorage.setItem(THEME_STORAGE_KEY, theme);
    } catch (_) {
      // ignore storage failures
    }
  }

  function toggleTheme() {
    const nextTheme = state.theme === 'dark' ? 'light' : 'dark';
    applyTheme(nextTheme);
    saveThemePreference(nextTheme);
    updateFloatingActions();
  }

  function loadAccentPreference() {
    try {
      return window.localStorage.getItem(ACCENT_STORAGE_KEY) || 'sky';
    } catch (_) {
      return 'sky';
    }
  }

  function applyAccent(accent) {
    state.accent = accent;
    document.documentElement.dataset.accent = accent;
  }

  function saveAccentPreference(accent) {
    try {
      window.localStorage.setItem(ACCENT_STORAGE_KEY, accent);
    } catch (_) {
      // ignore
    }
  }

  function setAccent(accent) {
    applyAccent(accent);
    saveAccentPreference(accent);
    updateAccentPicker();
    updateFloatingActions();
  }

  function toggleAccentPicker() {
    state.accentPickerOpen = !state.accentPickerOpen;
    if (state.accentPickerOpen) state.recentRoomsDrawerOpen = false;
    updateAccentPicker();
    updateRecentRoomsDrawer();
  }

  function closeAccentPicker() {
    if (!state.accentPickerOpen) return;
    state.accentPickerOpen = false;
    updateAccentPicker();
  }

  function updateAccentPicker() {
    let drawer = document.getElementById('accent-picker-drawer');
    if (!drawer) {
      drawer = document.createElement('div');
      drawer.id = 'accent-picker-drawer';
      app.appendChild(drawer);
    }
    // 先不加 open，让 CSS transition 能触发动画
    drawer.className = 'accent-picker-shell';
    drawer.innerHTML = `
      <div class="accent-picker-backdrop" data-action="close-accent-picker"></div>
      <aside class="accent-picker-drawer" role="dialog" aria-label="选择强调色">
        <div class="accent-picker-header">
          <span class="card-title">强调色</span>
          <button class="btn btn-ghost btn-sm" data-action="close-accent-picker" type="button">关闭</button>
        </div>
        <div class="accent-picker-body">
          ${ACCENT_PRESETS.map(p => `
            <button class="accent-swatch ${state.accent === p.id ? 'active' : ''}"
              data-action="set-accent" data-accent="${p.id}"
              style="background:${p.color}" title="${p.name}" type="button"
              aria-label="${p.name}"></button>
          `).join('')}
        </div>
      </aside>
    `;

    if (state.accentPickerOpen) {
      // 下一帧添加 open class 触发动画，同时定位
      requestAnimationFrame(() => {
        drawer.classList.add('open');
        const btn = document.querySelector('[data-action="toggle-accent-picker"]');
        const aside = drawer.querySelector('.accent-picker-drawer');
        if (btn && aside) {
          const btnRect = btn.getBoundingClientRect();
          aside.style.bottom = `${window.innerHeight - btnRect.top + 12}px`;
          aside.style.right = `${window.innerWidth - btnRect.left + 4}px`;
        }
      });
    }
  }

  function toggleRecentRoomsDrawer() {
    state.recentRoomsDrawerOpen = !state.recentRoomsDrawerOpen;
    if (state.recentRoomsDrawerOpen) state.accentPickerOpen = false;
    updateRecentRoomsDrawer();
    updateAccentPicker();
  }

  function closeRecentRoomsDrawer() {
    if (!state.recentRoomsDrawerOpen) return;
    state.recentRoomsDrawerOpen = false;
    updateRecentRoomsDrawer();
  }

  function isMobileLayout() {
    return window.matchMedia('(max-width: 900px)').matches;
  }

  function scrollMainToTop() {
    closeMobileFabMenu();
    const stream = document.getElementById('message-stream');
    if (stream && (stream.scrollHeight > stream.clientHeight || stream.scrollTop > 0)) {
      stream.scrollTo({ top: 0, behavior: 'smooth' });
      return;
    }
    window.scrollTo({ top: 0, behavior: 'smooth' });
  }

  function toggleMobileFabMenu() {
    if (!isMobileLayout()) return;
    state.mobileFabExpanded = !state.mobileFabExpanded;
    updateFloatingActions();
  }

  function closeMobileFabMenu() {
    if (!state.mobileFabExpanded) return;
    state.mobileFabExpanded = false;
    updateFloatingActions();
  }

  function toggleMobileQR() {
    if (!state.currentRoom) return;
    state.mobileQRVisible = !state.mobileQRVisible;
    if (state.mobileQRVisible) state.mobileRoomCardVisible = false;
    closeMobileFabMenu();
    updateSidebar();
    if (state.mobileQRVisible) updateQR();
  }

  function toggleMobileRoomCard() {
    if (!state.currentRoom) return;
    state.mobileRoomCardVisible = !state.mobileRoomCardVisible;
    if (state.mobileRoomCardVisible) state.mobileQRVisible = false;
    closeMobileFabMenu();
    updateSidebar();
  }

  function render() {
    if (!app) return;
    if (!rendered) {
      app.innerHTML = state.loading ? renderLoading() : renderShell();
      rendered = true;
      bindGlobalEvents();
    } else if (!state.loading) {
      const shell = document.getElementById('workspace-shell');
      if (!shell) {
        app.innerHTML = renderShell();
        bindGlobalEvents();
      }
    }

    if (state.loading) return;

    updateViewState();
    updateSidebar();
    updateMessages();
    updateConnectionStatus();
    updateBanners();
    updateConfirmDialog();
    updateAdminView();
    updateFloatingActions();
    updateRecentRoomsDrawer();
    updateAccentPicker();

    if (state.currentRoom) {
      updateQR();
      startLifecycleTimer();
    }
  }

  function renderLoading() {
    return `<div style="display:flex;align-items:center;justify-content:center;min-height:100vh"><div class="spinner"></div></div>`;
  }

  function renderShell() {
    return `
      <div id="workspace-shell">
        <header class="topbar">
          <div class="topbar-inner">
            <div class="brand">
              <div class="brand-mark">${renderIcon('friends_link_send_share_icon_123622', '云剪贴板', 'brand-icon')}</div>
              <span class="brand-name">云剪贴板</span>
            </div>
            <div class="topbar-actions">
              <span id="connection-badge" class="badge badge-muted"><span class="connection-dot offline"></span><span id="connection-label">离线</span></span>
              <button class="btn btn-ghost btn-sm" data-action="toggle-admin" type="button">${state.activeView === 'admin' ? '返回' : '管理'}</button>
            </div>
          </div>
        </header>
        <div class="main-content flip-container ${state.activeView !== 'workspace' ? 'flipped' : ''}" id="view-container">
          <div class="flip-inner app-flip-inner">
            <div class="flip-front app-view-face">
              <div class="workspace-grid ${!state.currentRoom ? 'is-disconnected' : ''} ${state.workspaceTransition ? `is-${state.workspaceTransition}` : ''}" id="workspace-grid">
                <aside class="sidebar ${!state.currentRoom ? 'is-disconnected' : ''} ${state.workspaceTransition ? `is-${state.workspaceTransition}` : ''}" id="sidebar"></aside>
                <section class="main-panel ${!state.currentRoom ? 'is-disconnected' : ''} ${state.workspaceTransition ? `is-${state.workspaceTransition}` : ''}" id="main-panel">
                  <div id="message-area"></div>
                </section>
              </div>
            </div>
            <div class="flip-back app-view-face">
              <section class="admin-view" id="admin-view"></section>
            </div>
          </div>
        </div>
        <div id="banners" class="toast-overlay"></div>
        <div class="floating-actions" id="floating-actions"></div>
        <div id="confirm-overlay" style="display:none"></div>
      </div>
    `;
  }

  function updateViewState() {
    const container = document.getElementById('view-container');
    if (container) container.classList.toggle('flipped', state.activeView !== 'workspace');
    const workspace = document.getElementById('workspace-grid');
    const sidebar = document.getElementById('sidebar');
    const mainPanel = document.getElementById('main-panel');
    if (workspace) {
      workspace.classList.toggle('is-disconnected', !state.currentRoom);
      workspace.classList.toggle('is-entering-room', state.workspaceTransition === 'entering-room');
    }
    if (sidebar) {
      sidebar.classList.toggle('is-disconnected', !state.currentRoom);
      sidebar.classList.toggle('is-entering-room', state.workspaceTransition === 'entering-room');
    }
    if (mainPanel) {
      mainPanel.classList.toggle('is-disconnected', !state.currentRoom);
      mainPanel.classList.toggle('is-entering-room', state.workspaceTransition === 'entering-room');
    }
    const adminBtn = document.querySelector('[data-action="toggle-admin"]');
    if (adminBtn) adminBtn.textContent = state.activeView === 'admin' ? '返回' : '管理';
  }

  function updateSidebar() {
    const el = document.getElementById('sidebar');
    if (!el) return;
    const showRoomCard = state.currentRoom && (!isMobileLayout() || state.mobileRoomCardVisible);
    const showQRCard = state.currentRoom && (!isMobileLayout() || state.mobileQRVisible);
    el.innerHTML = state.currentRoom
      ? `${showRoomCard ? renderRoomCard() : ''}${showQRCard ? renderRoomQRCard() : ''}`
      : renderWelcomeCard();
    if (state.currentRoom && showRoomCard) updateLifecycle();
    if (showQRCard) updateQR();
    const draftInput = document.getElementById('draft-input');
    if (draftInput) {
      draftInput.addEventListener('keydown', e => {
        if (e.key === 'Enter' && !e.shiftKey) {
          e.preventDefault();
          sendMessage();
        }
      });
      draftInput.addEventListener('input', () => {
        const counter = document.getElementById('compose-counter');
        if (counter) {
          counter.textContent = `${[...draftInput.value].length} / ${state.maxMessageTextLength}`;
        }
      });
      // 初始同步已有草稿的字符数
      const counter = document.getElementById('compose-counter');
      if (counter) {
        counter.textContent = `${[...draftInput.value].length} / ${state.maxMessageTextLength}`;
      }
      if (!isMobileLayout()) {
        setTimeout(() => {
          draftInput.focus();
          draftInput.setSelectionRange(draftInput.value.length, draftInput.value.length);
        }, 0);
      }
    }
  }

  function updateMessages() {
    const el = document.getElementById('message-area');
    if (!el) return;
    if (!state.currentRoom) {
      el.innerHTML = renderEmptyState();
      return;
    }

    const content = `
      <div class="message-stream" id="message-stream">
        ${state.messages.length === 0
        ? `<div class="empty-state" style="flex:0;padding:32px"><div class="empty-state-icon empty-state-icon-message">${renderIcon('menu', '暂无消息', 'empty-state-svg')}</div><p>还没有消息，发送第一条吧</p></div>`
        : state.messages.map(renderMessageCard).join('')}
      </div>
    `;

    const stream = document.getElementById('message-stream');
    if (stream) {
      const wasNearTop = stream.scrollTop < 80;
      el.innerHTML = content;
      const nextStream = document.getElementById('message-stream');
      if (nextStream && wasNearTop) nextStream.scrollTop = 0;
    } else {
      el.innerHTML = content;
    }
  }

  function updateConnectionStatus() {
    const badge = document.getElementById('connection-badge');
    const label = document.getElementById('connection-label');
    if (!badge || !label) return;
    const tone = S.statusTone(state.connectionStatus);
    let dotClass = 'offline';
    if (state.connectionStatus === 'websocket') dotClass = 'online';
    else if (state.connectionStatus === 'polling') dotClass = 'polling';
    else if (state.connectionStatus === 'connecting') dotClass = 'connecting';
    badge.className = `badge badge-${tone === 'brand' ? 'accent' : tone}`;
    const dot = badge.querySelector('.connection-dot');
    if (dot) dot.className = `connection-dot ${dotClass}`;
    label.textContent = state.connectionLabel;
  }

  function updateBanners() {
    const el = document.getElementById('banners');
    if (!el) return;
    const iconError = '<svg class="toast-icon" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="2"><circle cx="10" cy="10" r="8"/><line x1="7" y1="7" x2="13" y2="13"/><line x1="13" y1="7" x2="7" y2="13"/></svg>';
    const iconSuccess = '<svg class="toast-icon" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="2"><circle cx="10" cy="10" r="8"/><polyline points="6,10 9,13 14,7"/></svg>';
    const iconInfo = '<svg class="toast-icon" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="2"><circle cx="10" cy="10" r="8"/><line x1="10" y1="6" x2="10" y2="11"/><circle cx="10" cy="14" r="0.5" fill="currentColor" stroke="none"/></svg>';
    const parts = [];
    if (state.error) parts.push(`<div class="toast-card error">${iconError}<span>${esc(state.error)}</span></div>`);
    if (state.success) parts.push(`<div class="toast-card success">${iconSuccess}<span>${esc(state.success)}</span></div>`);
    if (state.notice) parts.push(`<div class="toast-card info">${iconInfo}<span>${esc(state.notice)}</span></div>`);
    el.innerHTML = parts.join('');
  }

  function updateLifecycle() {
    const el = document.getElementById('lifecycle-display');
    if (!el || !state.currentRoom) return;
    const room = state.currentRoom;
    if (room.isPermanent) {
      const parent = el.closest('.room-lifecycle');
      if (parent) parent.innerHTML = '<div class="lifecycle-row"><span class="lifecycle-value permanent">永不过期</span></div>';
      return;
    }
    const remaining = S.formatRemaining(room.deleteAfter);
    if (remaining) {
      el.textContent = remaining.text;
      el.className = 'lifecycle-value ' + remaining.cls + (remaining.sec < 3600 ? ' countdown-warning' : '');
    }
  }

  function updateQR() {
    const container = document.getElementById('qr-container');
    const fallback = document.getElementById('qr-fallback-link');
    if (!container || !state.currentRoom) return;
    const url = getRoomUrl(state.currentRoom.roomCode);
    if (fallback) fallback.textContent = url;
    if (container.dataset.qrUrl === url) return;
    container.dataset.qrUrl = url;
    container.innerHTML = '';

    try {
      if (typeof QRCode !== 'undefined') {
        const qr = new QRCode(container, {
          text: url,
          width: 140,
          height: 140,
          colorDark: '#0a0a0f',
          colorLight: '#ffffff',
          correctLevel: QRCode.CorrectLevel ? QRCode.CorrectLevel.M : 0,
        });
        if (qr && typeof qr.makeCode === 'function') qr.makeCode(url);
      } else {
        throw new Error('QRCode unavailable');
      }
    } catch (e) {
      console.warn('QR code generation failed:', e);
      container.innerHTML = '<div class="qr-fallback">二维码生成失败</div>';
    }
  }

  function updateConfirmDialog() {
    const el = document.getElementById('confirm-overlay');
    if (!el) return;
    if (state.confirmDialog) {
      el.style.display = 'flex';
      el.innerHTML = `
        <div class="confirm-dialog">
          <h3>${esc(state.confirmDialog.title)}</h3>
          <p>${esc(state.confirmDialog.message)}</p>
          <div class="confirm-dialog-actions">
            <button class="btn btn-secondary" data-action="cancel-confirm" type="button">取消</button>
            <button class="btn btn-danger" data-action="confirm-action" type="button">确认</button>
          </div>
        </div>`;
    } else {
      el.style.display = 'none';
    }
  }

  function updateAdminView() {
    const el = document.getElementById('admin-view');
    if (!el) return;
    el.innerHTML = `
      <div class="admin-view-shell card">
        <div class="admin-view-header">
          <div>
            <div class="card-title">管理员后台</div>
            <div class="card-subtitle">管理房间、查看统计信息并维护系统设置</div>
          </div>
          <button class="btn btn-ghost btn-sm" data-action="toggle-admin" type="button">返回工作区</button>
        </div>
        <div class="admin-view-body">
          ${state.authenticated ? renderAdminAuthed() : renderAdminLogin()}
        </div>
      </div>
    `;
    // 批量删除 checkbox 监听
    if (state.authenticated && state.adminTab !== 'settings') {
      requestAnimationFrame(() => attachAdminCheckListeners());
    }
    // 初始化 Chart.js 图表
    if (state.authenticated && state.adminTab !== 'settings') {
      requestAnimationFrame(() => initCharts());
    }
  }

  function updateFloatingActions() {
    const el = document.getElementById('floating-actions');
    if (!el) return;
    const isMobile = isMobileLayout();
    if (isMobile) {
      if (!state.currentRoom) {
        el.className = 'floating-actions mobile-fab-stack';
        el.innerHTML = `
          <div class="fab-mobile-shell">
            <div class="fab-primary-group">
              <button class="fab-button" data-action="toggle-recent-rooms" type="button" aria-label="最近房间" title="最近房间">${renderIcon('menu', '最近房间', 'fab-icon fab-icon-inverse')}</button>
              <button class="fab-button" data-action="toggle-accent-picker" type="button" aria-label="强调色" title="强调色">${renderIcon('palette', '强调色', 'fab-icon fab-icon-inverse')}</button>
              <button class="fab-button" data-action="toggle-theme" type="button" aria-label="切换主题" title="切换主题">${renderIcon(state.theme === 'dark' ? 'sun-medium' : 'moon', '切换主题', 'fab-icon fab-icon-inverse')}</button>
            </div>
          </div>
        `;
        return;
      }
      const expanded = state.mobileFabExpanded;
      const secondaryActions = state.currentRoom ? [
        {
          action: 'toggle-theme',
          label: '切换主题',
          icon: state.theme === 'dark' ? 'sun-medium' : 'moon',
        },
        {
          action: 'toggle-accent-picker',
          label: '强调色',
          icon: 'palette',
        },
        {
          action: 'toggle-mobile-qr',
          label: state.mobileQRVisible ? '隐藏二维码' : '显示二维码',
          icon: 'scan-qr-code',
        },
        {
          action: 'toggle-mobile-room-card',
          label: state.mobileRoomCardVisible ? '隐藏房间卡' : '显示房间卡',
          icon: 'milestone',
        }
      ] : [];
      const step = 60;
      const shift = secondaryActions.length * step;
      const secondaryButtons = secondaryActions.map((item) => `
        <button
          class="fab-button fab-button-brand fab-primary-button fab-expanded-button"
          data-action="${item.action}"
          type="button"
          aria-label="${item.label}"
          title="${item.label}"
        >${renderIcon(item.icon, item.label, 'fab-icon fab-icon-inverse')}</button>
      `).join('');
      el.className = `floating-actions mobile-fab-stack ${expanded ? 'expanded' : ''}`;
      const scrollTopBtn = state.showScrollTop ? `<button class="fab-button fab-button-brand fab-primary-button" data-action="scroll-top" type="button" aria-label="回到顶部" title="回到顶部">${renderIcon('chevron-up', '回到顶部', 'fab-icon fab-icon-inverse')}</button>` : '';
      el.innerHTML = `
        <div class="fab-mobile-shell">
          <div class="fab-primary-group ${expanded ? 'expanded' : ''}">
            ${expanded ? secondaryButtons : `<button class="fab-button fab-button-brand fab-plus-button" data-action="toggle-mobile-fab-menu" type="button" aria-label="更多操作" title="更多操作">${renderIcon('plus', '更多操作', 'fab-icon fab-icon-inverse')}</button>`}
            <button class="fab-button fab-button-brand fab-primary-button" data-action="toggle-recent-rooms" type="button" aria-label="最近房间" title="最近房间">${renderIcon('menu', '最近房间', 'fab-icon fab-icon-inverse')}</button>
            ${scrollTopBtn}
          </div>
        </div>
      `;
      return;
    }

    const buttons = [];
    if (state.showScrollTop) {
      buttons.push(`<button class="fab-button" data-action="scroll-top" type="button" aria-label="回到顶部" title="回到顶部">${renderIcon('chevron-up', '回到顶部', 'fab-icon fab-icon-inverse')}</button>`);
    }
    buttons.push(
      `<button class="fab-button" data-action="toggle-recent-rooms" type="button" aria-label="最近房间" title="最近房间">${renderIcon('menu', '最近房间', 'fab-icon fab-icon-inverse')}</button>`,
      `<button class="fab-button" data-action="toggle-accent-picker" type="button" aria-label="强调色" title="强调色">${renderIcon('palette', '强调色', 'fab-icon fab-icon-inverse')}</button>`,
      `<button class="fab-button" data-action="toggle-theme" type="button" aria-label="切换主题" title="切换主题">${renderIcon(state.theme === 'dark' ? 'sun-medium' : 'moon', '切换主题', 'fab-icon fab-icon-inverse')}</button>`
    );
    el.className = 'floating-actions';
    el.innerHTML = buttons.join('');
  }

  function updateRecentRoomsDrawer() {
    let drawer = document.getElementById('recent-rooms-drawer');
    if (!drawer) {
      drawer = document.createElement('div');
      drawer.id = 'recent-rooms-drawer';
      app.appendChild(drawer);
    }
    // 先不加 open，让 CSS transition 能触发动画
    drawer.className = 'recent-rooms-shell';
    drawer.innerHTML = `
      <div class="recent-rooms-backdrop" data-action="close-recent-rooms"></div>
      <aside class="recent-rooms-drawer" role="dialog" aria-label="最近房间">
        <div class="recent-rooms-header">
          <div>
            <div class="card-title">最近连接的房间</div>
            <div class="card-subtitle">保留最近 5 个房间，点击即可快速加入</div>
          </div>
          <button class="btn btn-ghost btn-sm" data-action="close-recent-rooms" type="button">关闭</button>
        </div>
        <div class="recent-rooms-list">
          ${state.recentRooms.length === 0
        ? '<div class="recent-room-empty">还没有最近连接记录</div>'
        : state.recentRooms.map(item => `
              <button class="recent-room-item" data-action="join-recent-room" data-room-code="${esc(item.roomCode)}" type="button">
                <span class="recent-room-code">${esc(item.roomName?.trim() || item.roomCode)}</span>
                ${item.roomName?.trim() ? `<span class="recent-room-subcode">${esc(item.roomCode)}</span>` : ''}
              </button>
            `).join('')}
        </div>
      </aside>
    `;

    if (state.recentRoomsDrawerOpen) {
      // 下一帧添加 open class 触发动画，同时定位
      requestAnimationFrame(() => {
        drawer.classList.add('open');
        const btn = document.querySelector('[data-action="toggle-recent-rooms"]');
        const aside = drawer.querySelector('.recent-rooms-drawer');
        if (btn && aside) {
          const btnRect = btn.getBoundingClientRect();
          const drawerWidth = 320;
          aside.style.bottom = `${window.innerHeight - btnRect.top + 12}px`;
          aside.style.right = `${window.innerWidth - btnRect.left + 4}px`;
          aside.style.maxWidth = `${Math.min(drawerWidth, btnRect.left - 16)}px`;
        }
      });
    }
  }

  function renderWelcomeCard() {
    return `
      <div class="card room-info welcome-card ${!state.currentRoom ? 'welcome-card-disconnected' : ''}">
        <div class="welcome-hero">
          <div class="welcome-brand-mark">${renderIcon('friends_link_send_share_icon_123622', '云剪贴板', 'welcome-brand-icon')}</div>
          <h1 class="welcome-title">云剪贴板</h1>
          <p class="welcome-tagline">跨设备即时同步，安全便捷</p>
        </div>
        <div class="welcome-actions">
          <button class="btn btn-primary btn-lg btn-block welcome-create-btn" data-action="create-room">
            ${renderIcon('plus', '创建', 'btn-icon btn-inline-icon-inverse')}
            <span class="welcome-btn-text">创建新房间</span>
            <span class="welcome-btn-hint">一键创建，即刻开始</span>
          </button>
          <div class="welcome-divider"><span>或</span></div>
          <div class="welcome-join-group">
            <div class="welcome-join-input-wrap">
              <input id="join-room-input" type="text" placeholder="输入房间号" value="${esc(state.roomCode)}" class="welcome-join-input" autocomplete="off" autocorrect="off" autocapitalize="off" spellcheck="false" onfocus="this.placeholder=''" onblur="this.placeholder='输入房间号'" />
              <button class="btn btn-secondary welcome-join-btn" data-action="join-room">
                ${renderIcon('arrow-right', '加入', 'btn-icon')} 加入
              </button>
            </div>
          </div>
        </div>
      </div>`;
  }

  function renderRoomCard() {
    const room = state.currentRoom;
    const remaining = S.formatRemaining(room.deleteAfter);
    const extendThresholdSec = state.publicSettings?.roomCanExtendSec || 12 * 3600;
    const extendHours = Math.round((state.publicSettings?.roomExtendSec || 24 * 3600) / 3600);
    const canExt = room.isPermanent ? false : S.canExtend(remaining ? remaining.sec : null, extendThresholdSec);
    const displayName = getRoomDisplayName(room);
    const fileQueue = state.filesToUpload || [];

    return `
      <div class="card room-info-card">
        <!-- 房间名称 + 元信息 左侧，房间号 右侧跨行 -->
        <div class="room-top-area">
          <div class="room-info-left">
            <div class="room-header-row">
              ${state.roomNameEditing ? `
                <div class="room-name-editor-inline">
                  <input id="room-name-input" type="text" maxlength="40" value="${esc(room.roomName || '')}" placeholder="输入房间名称" />
                  <div class="room-name-editor-actions">
                    <button class="btn btn-primary btn-xs" data-action="save-room-name">保存</button>
                    <button class="btn btn-ghost btn-xs" data-action="cancel-edit-room-name">取消</button>
                  </div>
                </div>
              ` : `
                <span class="room-name-title" title="点击编辑房间名称">${esc(displayName)}</span>
                <button class="btn btn-icon-only btn-xs" data-action="toggle-edit-room-name" title="编辑名称">${renderIcon('edit', '编辑', 'btn-icon')}</button>
              `}
            </div>
            <div class="room-meta-row">
              ${room.isPermanent
                ? '<span class="lifecycle-text permanent">长期房间 · 永不过期</span>'
                : (remaining ? `<span class="lifecycle-text ${remaining.cls} ${remaining.sec < 3600 ? 'countdown-warning' : ''}" id="lifecycle-display">有效期 ${remaining.text}</span>` : '')}
            </div>
          </div>
          <code class="room-code-text" data-action="copy-room-code" title="点击复制房间号">${esc(room.roomCode)}</code>
        </div>

        <!-- 操作按钮 -->
        <div class="room-actions-row">
          ${canExt ? `<button class="btn btn-secondary btn-sm" data-action="extend-room">续期 ${extendHours}h</button>` : ''}
          ${!room.isPermanent ? '<button class="btn btn-secondary btn-sm" data-action="permanent-room">设为长期</button>' : ''}
          <button class="btn btn-danger btn-sm" data-action="destroy-room">销毁</button>
          <div class="room-actions-spacer"></div>
          <button class="btn btn-outline-danger btn-sm" data-action="leave-room">退出房间</button>
        </div>

        <!-- 消息编辑区 -->
        <div class="message-compose" style="margin-top:var(--space-4)">
          <div class="compose-area">
            ${fileQueue.length > 0 ? `
              <div class="compose-file-preview">
                <div class="compose-file-header">
                  <span class="compose-file-count">待发送 ${fileQueue.length} 个文件</span>
                  <button class="btn btn-ghost btn-xs" data-action="clear-upload-files">清空</button>
                </div>
                <div class="compose-file-list">
                  ${fileQueue.map((file, i) => `
                    <div class="compose-file-item">
                      ${isImageFile(file) ? `<img src="${URL.createObjectURL(file)}" alt="${esc(file.name)}" class="compose-file-thumb" />` : ''}
                      <span class="compose-file-item-name">${esc(file.name)}</span>
                      <span class="compose-file-item-size">${formatSize(file.size)}</span>
                      <button class="btn btn-ghost btn-xs" data-action="remove-upload-file" data-file-index="${i}">移除</button>
                    </div>
                  `).join('')}
                </div>
                ${uploadInFlight ? `<div class="upload-progress"><div class="upload-progress-bar" style="width:${state.uploadProgressPct}%"></div></div><div class="upload-progress-meta">${state.uploadProgressPct}% · ${formatSize(state.uploadTransferredBytes)} · ${formatSpeed(state.uploadSpeedBps)}</div>` : ''}
              </div>
            ` : `
              <textarea id="draft-input" rows="3" placeholder="输入文本消息... (Enter 发送，Shift+Enter 换行)">${esc(state.draft)}</textarea>
            `}
            <div class="compose-toolbar">
              <button class="btn btn-secondary btn-sm" id="upload-trigger-btn" data-action="trigger-upload" title="选择文件">
                ${renderIcon('plus', '上传', 'btn-icon')} 选择文件
              </button>
              <input id="file-upload-input" type="file" multiple style="display:none" />
              ${fileQueue.length > 0 ? `
                <button class="btn btn-primary btn-sm" data-action="upload-file">上传 ${fileQueue.length} 个文件</button>
                <span class="compose-counter">${formatSizeForLimit(state.uploadLimitBytes)}</span>
              ` : `
                <button class="btn btn-primary btn-sm" data-action="send-message">${renderIcon('plus', '发送', 'btn-icon btn-inline-icon-inverse')} 发送消息</button>
                <span class="compose-counter" id="compose-counter">0 / ${state.maxMessageTextLength}</span>
              `}
            </div>
          </div>
        </div>
      </div>`;
  }

  function renderRoomQRCard() {
    if (!state.currentRoom) return '';
    return `
      <div class="card qr-room-card">
        <div class="card-title">扫码进入房间</div>
        <div class="card-subtitle qr-card-subtitle">手机打开、分享或稍后在其他设备继续进入当前房间</div>
        <div class="qr-section qr-section-standalone">
          <div id="qr-container" class="qr-code"></div>
          <span class="qr-label">扫一扫即可快速加入</span>
        </div>
        <div class="share-link qr-link-block" id="qr-fallback-link">${esc(getRoomUrl(state.currentRoom.roomCode))}</div>
        <div class="qr-actions">
          <button class="btn btn-primary btn-lg btn-block qr-action-primary" data-action="copy-room-link">${renderIcon('copy', '复制链接', 'btn-icon btn-inline-icon-inverse')} 复制房间链接</button>
        </div>
      </div>
    `;
  }

  function renderAdminLogin() {
    return `
      <div class="admin-login-stack">
        <input id="admin-password" type="password" placeholder="管理员密码" />
        <button class="btn btn-primary" data-action="admin-login">登录管理后台</button>
        <div class="github-star-block">
          <a href="https://github.com/AndreasByrsting/cloud-clipboard" target="_blank" rel="noopener" class="github-star-link">
            <svg class="github-star-icon" viewBox="0 0 16 16" width="16" height="16" fill="currentColor"><path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0016 8c0-4.42-3.58-8-8-8z"/></svg>
            Star on GitHub
          </a>
        </div>
      </div>`;
  }

  function renderAdminAuthed() {
    const s = state.adminSettings || {};
    const keyLabels = {
      roomCodeLength: '房间号长度',
      roomDefaultTTLSec: '房间有效期',
      roomExtendSec: '房间续期时间',
      roomCanExtendSec: '房间可续期阈值',
      fileMessageExpireSec: '文件消息过期时间',
      fileNeverExpire: '文件永不过期',
      maxUploadBytes: '文件上传大小限制',
      maxMessageTextLength: '文字消息长度限制',
    };
    const keys = Object.keys(keyLabels).filter(k => !BOOL_SETTING_KEYS.has(k)).sort();
    return `
      <div class="admin-tabs">
        <button class="btn btn-sm ${state.adminTab !== 'settings' ? 'btn-primary' : 'btn-ghost'}" data-action="admin-tab-rooms">房间管理</button>
        <button class="btn btn-sm ${state.adminTab === 'settings' ? 'btn-primary' : 'btn-ghost'}" data-action="admin-tab-settings">系统设置</button>
      </div>
      <div class="admin-view-body-inner">
        ${state.adminTab !== 'settings' ? `
          ${state.adminStats ? `
          <div class="admin-stats-row">
            <div class="admin-stats-grid">
              <div class="admin-stat-card">
                <div class="admin-stat-value">${state.adminStats.totalRooms}</div>
                <div class="admin-stat-label">房间总数</div>
              </div>
              <div class="admin-stat-card">
                <div class="admin-stat-value">${state.adminStats.permanentRooms}</div>
                <div class="admin-stat-label">长期房间</div>
              </div>
              <div class="admin-stat-card">
                <div class="admin-stat-value">${state.adminStats.totalMessages}</div>
                <div class="admin-stat-label">消息总数</div>
              </div>
              <div class="admin-stat-card">
                <div class="admin-stat-value">${state.adminStats.totalFiles}</div>
                <div class="admin-stat-label">文件总数</div>
              </div>
            </div>
            <div class="admin-charts-row">
              <div class="admin-chart-card">
                <div class="admin-chart-title">24h 房间创建趋势</div>
                <div class="admin-chart-wrap">${renderChartCanvas(state.adminStats.roomChart || [], 'room-chart', '--accent')}</div>
              </div>
              <div class="admin-chart-card">
                <div class="admin-chart-title">24h 消息创建趋势</div>
                <div class="admin-chart-wrap">${renderChartCanvas(state.adminStats.messageChart || [], 'msg-chart', '--success')}</div>
              </div>
            </div>
          </div>
          ` : ''}
          <div class="admin-room-list" id="admin-room-list">
            ${state.adminRooms.length === 0 ? '<div style="color:var(--text-muted);font-size:0.85rem;text-align:center;padding:12px">暂无房间</div>' : ''}
            ${state.adminRooms.length > 0 ? `
              <div class="admin-room-list-header">
                <label class="admin-room-check-label">
                  <input type="checkbox" id="admin-select-all" />
                  <span style="font-size:0.8rem;color:var(--text-muted)">全选本页</span>
                </label>
                <button class="btn btn-danger btn-xs" id="admin-batch-delete-btn" style="display:none" data-action="admin-batch-delete">批量删除</button>
              </div>
            ` : ''}
            ${state.adminRooms.map(r => {
              const remaining = S.formatRemaining(r.deleteAfter);
              return `
              <div class="admin-room-item">
                <label class="admin-room-check-label">
                  <input type="checkbox" class="admin-room-check" data-room-code="${esc(r.roomCode)}" />
                </label>
                <div class="admin-room-info">
                  <div class="admin-room-name-row">
                    <span class="admin-room-name">${esc(r.roomName?.trim() || r.roomCode)}</span>
                    ${r.isPermanent ? '<span class="badge badge-success">长期</span>' : (remaining ? `<span class="admin-room-remaining ${remaining.cls}">剩余 ${remaining.text}</span>` : '')}
                  </div>
                  <div class="admin-room-meta-row">
                    ${r.roomName?.trim() ? `<span class="admin-room-code">${esc(r.roomCode)}</span>` : ''}
                    <span class="admin-room-date">${fmt(r.createdAt)}</span>
                  </div>
                </div>
                <div class="admin-room-actions">
                  <button class="btn btn-ghost btn-xs" data-action="admin-enter" data-room-code="${esc(r.roomCode)}">进入</button>
                  <button class="btn btn-danger btn-xs" data-action="admin-delete" data-room-code="${esc(r.roomCode)}">删除</button>
                </div>
              </div>
            `}).join('')}
            ${state.adminTotalRooms > 0 ? renderAdminPagination() : ''}
          </div>
        ` : `
          <div class="admin-settings-grid">
            ${keys.map(k => `
              <div class="settings-field">
                <label>${keyLabels[k]}</label>
                ${DURATION_SETTING_KEYS.has(k)
            ? `<div class="input-with-suffix"><input type="number" class="settings-input" data-setting-key="${k}" value="${s[k] !== undefined ? s[k] : ''}" min="0.5" step="0.5" /><span class="input-suffix">小时</span></div>`
            : BYTE_SETTING_KEYS.has(k)
              ? `<div class="input-with-suffix"><input type="number" class="settings-input" data-setting-key="${k}" value="${s[k] !== undefined ? s[k] : ''}" min="1" step="1" /><span class="input-suffix">MB</span></div>`
              : `<input type="number" class="settings-input" data-setting-key="${k}" value="${s[k] !== undefined ? s[k] : ''}" min="1" step="1" />`}
              </div>
            `).join('')}
          </div>
          <div class="admin-settings-bool">
            <label class="settings-check-label">
              <input type="checkbox" class="settings-checkbox" data-setting-key="fileNeverExpire" ${s.fileNeverExpire ? 'checked' : ''} />
              <span>文件永不过期</span>
            </label>
            <span class="settings-check-hint">开启后，上传的文件不会自动过期，工作区不显示过期时间</span>
          </div>
          <div class="admin-settings-text">
            <div class="settings-field">
              <label>首页空态标题</label>
              <input type="text" class="settings-input" data-setting-key="emptyStateTitle" value="${esc(s.emptyStateTitle || '')}" maxlength="60" />
            </div>
            <div class="settings-field">
              <label>首页空态内容</label>
              <textarea class="settings-textarea" data-setting-key="emptyStateBody" rows="6" maxlength="600">${esc(s.emptyStateBody || '')}</textarea>
            </div>
          </div>
          <button class="btn btn-primary" data-action="admin-save-settings">保存设置</button>
          <div class="password-panel">
            <div class="card-title">修改管理员密码</div>
            <div class="admin-settings-grid">
              <div class="settings-field">
                <label>新密码</label>
                <input id="admin-new-password" type="password" value="${esc(state.adminNewPassword)}" placeholder="至少 8 位" />
              </div>
              <div class="settings-field">
                <label>再次输入新密码</label>
                <input id="admin-confirm-password" type="password" value="${esc(state.adminConfirmPassword)}" placeholder="再次输入新密码" />
              </div>
            </div>
            ${state.adminPasswordFeedback ? `<div class="password-feedback password-feedback-${esc(state.adminPasswordFeedbackType || 'error')}">${esc(state.adminPasswordFeedback)}</div>` : ''}
            <button class="btn btn-secondary" data-action="admin-change-password">更新密码</button>
          </div>
        `}
        <div style="display:flex;align-items:center;justify-content:space-between;margin-top:16px;padding-top:12px;border-top:1px solid var(--border-soft)">
          <span class="badge badge-accent">已登录</span>
          <button class="btn btn-ghost btn-sm" data-action="admin-logout">退出</button>
        </div>
      </div>`;
  }

  function renderChartCanvas(data, chartId, colorVar) {
    if (!data || data.length === 0) {
      return `<div class="admin-chart-empty">暂无数据</div>`;
    }
    const allZero = data.every(d => d.count === 0);
    if (allZero) {
      return `<div class="admin-chart-empty">暂无数据</div>`;
    }
    // 将数据存为 JSON，供 Chart.js 初始化时读取
    const encoded = JSON.stringify(data);
    return `<canvas class="admin-chart-canvas" id="${chartId}" data-chart-data="${esc(encoded)}" data-chart-color="${esc(colorVar)}"></canvas>`;
  }

  /** 当前活跃的 Chart.js 实例，key 为 canvas id */
  const chartInstances = {};

  function destroyChart(id) {
    if (chartInstances[id]) {
      chartInstances[id].destroy();
      delete chartInstances[id];
    }
  }

  function initCharts() {
    if (typeof Chart === 'undefined') return;
    const style = getComputedStyle(document.documentElement);
    const gridColor = style.getPropertyValue('--border-soft').trim() || '#e2e8f0';
    const textColor = style.getPropertyValue('--text-muted').trim() || '#94a3b8';

    ['room-chart', 'msg-chart'].forEach(id => {
      const canvas = document.getElementById(id);
      if (!canvas) return;
      destroyChart(id);

      const raw = canvas.dataset.chartData;
      const colorVar = canvas.dataset.chartColor;
      if (!raw) return;
      let data;
      try { data = JSON.parse(raw); } catch (e) { return; }
      if (!data || data.length === 0) return;

      const lineColor = style.getPropertyValue(colorVar).trim() || '#0ea5e9';
      const labels = data.map(d => d.hour);
      const values = data.map(d => d.count);
      const ctx = canvas.getContext('2d');

      // 构建稀疏 x 轴标签：只显示有数据点 + 首尾
      const sparseLabels = labels.map((l, i) => {
        if (i === 0 || i === labels.length - 1 || values[i] > 0) return l;
        return '';
      });

      const gradient = ctx.createLinearGradient(0, 0, 0, 160);
      gradient.addColorStop(0, lineColor + '30');
      gradient.addColorStop(1, lineColor + '00');

      chartInstances[id] = new Chart(ctx, {
        type: 'line',
        data: {
          labels: sparseLabels,
          datasets: [{
            data: values,
            borderColor: lineColor,
            backgroundColor: gradient,
            borderWidth: 2,
            pointRadius: values.map(v => v > 0 ? 3 : 0),
            pointBackgroundColor: lineColor,
            pointBorderColor: lineColor,
            pointBorderWidth: 0,
            pointHoverRadius: 5,
            tension: 0.3,
            fill: true,
          }],
        },
        options: {
          responsive: true,
          maintainAspectRatio: false,
          animation: { duration: 300 },
          interaction: { intersect: false, mode: 'index' },
          plugins: {
            legend: { display: false },
            tooltip: {
              backgroundColor: style.getPropertyValue('--bg-card').trim() || '#fff',
              titleColor: style.getPropertyValue('--text-primary').trim() || '#1e293b',
              bodyColor: style.getPropertyValue('--text-primary').trim() || '#1e293b',
              borderColor: gridColor,
              borderWidth: 1,
              cornerRadius: 6,
              displayColors: false,
              callbacks: {
                title: function(ts) { return labels[ts[0].dataIndex]; },
                label: function(t) { return '数量: ' + t.raw; },
              },
            },
          },
          scales: {
            x: {
              grid: { display: false },
              ticks: {
                color: textColor,
                font: { size: 10 },
                maxRotation: 0,
                autoSkip: false,
              },
            },
            y: {
              beginAtZero: true,
              grid: { color: gridColor, drawBorder: false },
              ticks: {
                color: textColor,
                font: { size: 10 },
                precision: 0,
                stepSize: 1,
              },
            },
          },
        },
      });
    });
  }

  function renderAdminPagination() {
    const totalPages = Math.ceil(state.adminTotalRooms / state.adminPageSize) || 1;
    const pageSizes = [10, 20, 50, 100, 500];
    return `
      <div class="admin-pagination">
        <div class="admin-pagination-info">
          共 ${state.adminTotalRooms} 个房间，第 ${state.adminPage} / ${totalPages} 页
        </div>
        <div class="admin-pagination-controls">
          <select class="admin-page-size-select" id="admin-page-size-select">
            ${pageSizes.map(s => `<option value="${s}" ${state.adminPageSize === s ? 'selected' : ''}>每页 ${s} 个</option>`).join('')}
          </select>
          <button class="btn btn-ghost btn-xs" data-action="admin-page-prev" ${state.adminPage <= 1 ? 'disabled' : ''}>上一页</button>
          <button class="btn btn-ghost btn-xs" data-action="admin-page-next" ${state.adminPage >= totalPages ? 'disabled' : ''}>下一页</button>
        </div>
      </div>`;
  }

  function renderEmptyState() {
    return `
      <div class="empty-state ${!state.currentRoom ? 'empty-state-disconnected' : ''}">
        <div class="empty-state-icon">${renderIcon('search', '空状态', 'empty-state-svg')}</div>
        <h3>${esc(getEmptyStateTitle())}</h3>
        <p>${esc(getEmptyStateBody())}</p>
      </div>`;
  }

  function renderMessageCard(msg) {
    const isFile = msg.type === 'file';
    const isImage = isImageMessage(msg);
    const fileExpired = isFile && msg.expiresAt && S.formatRemaining(msg.expiresAt).sec <= 0;
    const expanded = isMessageExpanded(msg.id);
    const textTooLong = !isFile && ((msg.textContent || '').split('\n').length > 3 || (msg.textContent || '').length > 200);
    const expandable = (!isFile && textTooLong) || (isImage && !fileExpired);
    return `
      <div class="message-card animate-in ${msg.isPinned ? 'pinned' : ''} ${expanded ? 'expanded' : ''} ${fileExpired ? 'file-expired' : ''}" id="msg-${msg.id}">
        <div class="message-card-header">
          <div class="message-meta">
            <span class="message-id">#${msg.id}</span>
            ${msg.isPinned ? '<span class="pin-badge">置顶</span>' : ''}
            <span class="badge badge-muted">${isFile ? '文件' : '文本'}</span>
            ${isFile && msg.expiresAt ? (() => { const fr = S.formatRemaining(msg.expiresAt); return fr.sec <= 0 ? '<span class="file-expiry-badge file-expiry-danger">已过期</span>' : `<span class="file-expiry-badge">将在 ${S.formatDateTime(msg.expiresAt)} 过期</span>`; })() : ''}
          </div>
          <span class="message-time">${fmt(msg.createdAt)}</span>
        </div>
        <div class="message-body${expandable ? ' message-body-clickable' : ''}" data-action="toggle-message-expand" data-msg-id="${msg.id}">
          ${isImage && !fileExpired
        ? `<div class="image-message ${expanded ? 'expanded' : 'collapsed'}"><img src="${api.downloadFile(msg.id)}" alt="${esc(msg.fileName || '图片')}" class="image-message-preview" loading="lazy" /></div>`
        : isImage && fileExpired
          ? `<div class="message-file file-expired-card"><span class="message-file-name">${esc(msg.fileName || '未命名文件')}</span><span class="message-file-size">${formatSize(msg.fileSize)}</span><span class="file-expired-hint">文件已过期，无法预览</span></div>`
        : isFile
          ? `<div class="message-file ${fileExpired ? 'file-expired-card' : ''}"><span class="message-file-name">${esc(msg.fileName || '未命名文件')}</span><span class="message-file-size">${formatSize(msg.fileSize)}</span>${fileExpired ? '<span class="file-expired-hint">文件已过期</span>' : ''}</div>`
          : `<pre class="message-text ${expanded ? 'expanded' : 'collapsed'}">${linkifyText(msg.textContent || '')}</pre>`}
          ${expandable ? `<div class="message-expand-hint">${expanded ? '点击收起' : '点击展开'}</div>` : ''}
        </div>
        <div class="message-actions">
          ${!isFile ? `<button class="msg-action-btn" data-action="copy-message" data-msg-id="${msg.id}" title="复制">${renderIcon('copy', '复制', 'msg-action-icon')}</button>` : ''}
          ${isFile && !fileExpired ? `<a href="${api.downloadFile(msg.id)}" class="msg-action-btn" download title="下载">${renderIcon('download', '下载', 'msg-action-icon')}</a>` : ''}
          <button class="msg-action-btn" data-action="pin-message" data-msg-id="${msg.id}" title="${msg.isPinned ? '取消置顶' : '置顶'}">${renderIcon('pin', '置顶', 'msg-action-icon')}</button>
          <button class="msg-action-btn msg-action-danger" data-action="delete-message" data-msg-id="${msg.id}" title="删除">${renderIcon('trash', '删除', 'msg-action-icon')}</button>
        </div>
      </div>`;
  }

  function bindGlobalEvents() {
    if (globalEventsBound) return;
    globalEventsBound = true;

    app.addEventListener('click', e => {
      if (state.mobileFabExpanded && !e.target.closest('#floating-actions')) {
        closeMobileFabMenu();
      }
      if (state.accentPickerOpen && !e.target.closest('#accent-picker-drawer') && !e.target.closest('[data-action="toggle-accent-picker"]')) {
        closeAccentPicker();
      }
      const btn = e.target.closest('[data-action]');
      if (!btn) return;
      // 如果点击的是消息中的外部链接，不要拦截，让浏览器打开新标签页
      if (e.target.closest('a[target="_blank"]')) return;
      const action = btn.dataset.action;
      e.preventDefault();

      switch (action) {
        case 'create-room': createRoom(); break;
        case 'join-room': joinRoom(); break;
        case 'send-message': sendMessage(); break;
        case 'upload-file': uploadFile(); break;
        case 'extend-room': extendRoom(); break;
        case 'permanent-room': setPermanentRoom(); break;
        case 'destroy-room': showConfirm('销毁房间', `确认销毁房间 ${state.currentRoom?.roomCode}？此操作不可撤销，所有消息和文件将被永久删除。`, destroyRoom); break;
        case 'toggle-admin': state.activeView === 'admin' ? closeAdminView() : openAdminView(); break;
        case 'leave-room': leaveRoomFromTopbar(); break;
        case 'admin-login': adminLogin(); break;
        case 'admin-logout': adminLogout(); break;
        case 'cancel-confirm': cancelConfirm(); break;
        case 'confirm-action': confirmAction(); break;
        case 'admin-enter': enterRoom(btn.dataset.roomCode, { syncHash: true, afterEnter: () => closeAdminView() }); break;
        case 'admin-delete': showConfirm('管理员删除房间', `确认删除房间 ${btn.dataset.roomCode}？`, () => adminDeleteRoom(btn.dataset.roomCode)); break;
        case 'admin-batch-delete': handleAdminBatchDelete(); break;
        case 'pin-message': togglePin(Number(btn.dataset.msgId)); break;
        case 'toggle-message-expand': toggleMessageExpanded(Number(btn.dataset.msgId)); break;
        case 'delete-message': deleteMessage(Number(btn.dataset.msgId)); break;
        case 'copy-message': copyMessage(Number(btn.dataset.msgId)); break;
        case 'copy-room-code': copyRoomCode(); break;
        case 'copy-room-link': copyRoomLink(); break;
        case 'scroll-top': scrollMainToTop(); break;
        case 'toggle-theme': toggleTheme(); break;
        case 'toggle-recent-rooms': toggleRecentRoomsDrawer(); break;
        case 'close-recent-rooms': closeRecentRoomsDrawer(); break;
        case 'toggle-accent-picker': toggleAccentPicker(); break;
        case 'close-accent-picker': closeAccentPicker(); break;
        case 'set-accent': setAccent(btn.dataset.accent); break;
        case 'join-recent-room': joinRecentRoom(btn.dataset.roomCode); break;
        case 'toggle-mobile-qr': toggleMobileQR(); break;
        case 'toggle-mobile-room-card': toggleMobileRoomCard(); break;
        case 'toggle-mobile-fab-menu': toggleMobileFabMenu(); break;
        case 'trigger-upload': document.getElementById('file-upload-input')?.click(); break;
        case 'remove-upload-file': removeUploadFile(Number(btn.dataset.fileIndex)); break;
        case 'clear-upload-files': clearUploadFiles(); break;
        case 'save-room-name': saveRoomName(); break;
        case 'toggle-edit-room-name': toggleEditRoomName(); break;
        case 'cancel-edit-room-name': cancelEditRoomName(); break;
        case 'admin-tab-rooms': state.adminTab = 'rooms'; updateAdminView(); break;
        case 'admin-tab-settings': state.adminTab = 'settings'; loadAdminSettings(); break;
        case 'admin-save-settings': saveAdminSettings(); break;
        case 'admin-change-password': changeAdminPassword(); break;
        case 'admin-page-prev': if (state.adminPage > 1) { state.adminPage--; loadAdminRooms(); } break;
        case 'admin-page-next': state.adminPage++; loadAdminRooms(); break;
      }
    });

    app.addEventListener('input', e => {
      if (e.target.id === 'draft-input') { state.draft = e.target.value; }
      if (e.target.id === 'join-room-input') state.roomCode = e.target.value.trim().toUpperCase();
      if (e.target.id === 'admin-password') state.adminPassword = e.target.value;
      if (e.target.id === 'admin-new-password') {
        state.adminNewPassword = e.target.value;
        if (state.adminConfirmPassword && state.adminNewPassword !== state.adminConfirmPassword) {
          state.adminPasswordFeedback = '两次输入的新密码不一致';
          state.adminPasswordFeedbackType = 'error';
        } else if (state.adminPasswordFeedbackType === 'error') {
          state.adminPasswordFeedback = '';
          state.adminPasswordFeedbackType = '';
        }
      }
      if (e.target.id === 'admin-confirm-password') {
        state.adminConfirmPassword = e.target.value;
        if (state.adminConfirmPassword && state.adminNewPassword !== state.adminConfirmPassword) {
          state.adminPasswordFeedback = '两次输入的新密码不一致';
          state.adminPasswordFeedbackType = 'error';
        } else if (state.adminConfirmPassword) {
          state.adminPasswordFeedback = '';
          state.adminPasswordFeedbackType = '';
        }
      }
      if (e.target.id === 'room-name-input' && state.currentRoom) {
        state.currentRoom.roomName = e.target.value;
      }
      if (e.target.classList?.contains('settings-input') || e.target.classList?.contains('settings-textarea')) {
        const key = e.target.dataset.settingKey;
        if (key === 'emptyStateTitle' || key === 'emptyStateBody') {
          state.adminSettings = state.adminSettings || {};
          state.adminSettings[key] = e.target.value;
        }
      }
    });

    document.addEventListener('keydown', e => {
      if (e.target.id === 'join-room-input' && e.key === 'Enter') {
        e.preventDefault(); joinRoom();
      }
      if (e.target.id === 'room-name-input' && e.key === 'Enter') {
        e.preventDefault(); saveRoomName();
      }
      if (e.target.id === 'admin-password' && e.key === 'Enter') {
        e.preventDefault(); adminLogin();
      }
      if ((e.target.id === 'admin-new-password' || e.target.id === 'admin-confirm-password') && e.key === 'Enter') {
        e.preventDefault(); changeAdminPassword();
      }
      // 全局 Enter：有待发送文件时触发上传，否则聚焦到输入框
      if (e.key === 'Enter' && !e.shiftKey && !e.ctrlKey && !e.metaKey) {
        if (state.currentRoom) {
          if (state.filesToUpload && state.filesToUpload.length > 0) {
            if (e.target.closest('.compose-file-preview') || e.target.closest('.message-compose') || e.target === document.body) {
              e.preventDefault();
              uploadFile();
            }
          }
        }
      }
      if (e.key === 'Escape' && state.mobileFabExpanded) {
        closeMobileFabMenu();
        return;
      }
      if (e.key === 'Escape' && state.recentRoomsDrawerOpen) {
        closeRecentRoomsDrawer();
        return;
      }
      if (e.key === 'Escape' && state.accentPickerOpen) {
        closeAccentPicker();
        return;
      }
      if (e.key === 'Escape' && state.mobileQRVisible) {
        state.mobileQRVisible = false;
        updateSidebar();
        updateFloatingActions();
      }
      if (e.key === 'Escape' && state.mobileRoomCardVisible) {
        state.mobileRoomCardVisible = false;
        updateSidebar();
        updateFloatingActions();
      }
    });

    app.addEventListener('change', e => {
      if (e.target.id === 'file-upload-input') {
        const files = Array.from(e.target.files || []);
        state.filesToUpload = [...(state.filesToUpload || []), ...files];
        updateSidebar();
      }
    });

    app.addEventListener('dragenter', e => { e.preventDefault(); const zone = e.target.closest('.message-compose'); if (zone) zone.classList.add('dragover'); });
    app.addEventListener('dragover', e => { e.preventDefault(); });
    app.addEventListener('dragleave', e => { e.preventDefault(); const zone = e.target.closest('.message-compose'); if (zone && !e.relatedTarget?.closest('.message-compose')) zone.classList.remove('dragover'); });
    app.addEventListener('drop', e => {
      e.preventDefault();
      const zone = e.target.closest('.message-compose');
      if (zone) zone.classList.remove('dragover');
      const files = Array.from(e.dataTransfer?.files || []);
      if (files.length > 0) {
        state.filesToUpload = [...(state.filesToUpload || []), ...files];
        updateSidebar();
      }
    });
    document.addEventListener('paste', e => {
      if (!state.currentRoom) return;
      const activeTag = document.activeElement?.tagName?.toLowerCase();
      if (activeTag === 'input' || activeTag === 'textarea') return;
      const items = Array.from(e.clipboardData?.items || []);
      const files = items
        .filter(item => item.kind === 'file')
        .map(item => item.getAsFile())
        .filter(Boolean);
      if (files.length > 0) {
        e.preventDefault();
        state.filesToUpload = [...(state.filesToUpload || []), ...files];
        updateSidebar();
        banner('notice', `已添加 ${files.length} 个文件到待上传列表`);
        return;
      }
      const text = e.clipboardData?.getData('text/plain')?.trim();
      if (text) {
        e.preventDefault();
        state.draft = state.draft ? state.draft + '\n' + text : text;
        updateSidebar();
        const draftInput = document.getElementById('draft-input');
        if (draftInput) {
          draftInput.value = state.draft;
          draftInput.focus();
        }
      }
    });

    const handleScroll = () => {
      closeMobileFabMenu();
      const threshold = window.innerHeight * 3;
      const scrolled = window.scrollY > threshold;
      if (state.showScrollTop !== scrolled) {
        state.showScrollTop = scrolled;
        updateFloatingActions();
      }
    };
    window.addEventListener('scroll', handleScroll, { passive: true });
    app.addEventListener('scroll', handleScroll, { passive: true, capture: true });
  }

  function cancelConfirm() {
    state.confirmDialog = null;
    updateConfirmDialog();
  }

  function confirmAction() {
    const action = state.confirmDialog?.action;
    state.confirmDialog = null;
    updateConfirmDialog();
    if (action) {
      Promise.resolve(action()).catch(e => banner('error', e?.message || '操作失败'));
    }
  }

  function showConfirm(title, message, action) {
    state.confirmDialog = { title, message, action };
    updateConfirmDialog();
    const overlay = document.getElementById('confirm-overlay');
    if (overlay) overlay.style.display = 'flex';
  }

  function copyRoomCode() {
    if (!state.currentRoom) return;
    copyToClipboard(state.currentRoom.roomCode, '房间号已复制');
  }

  function copyRoomLink() {
    if (!state.currentRoom) return;
    copyToClipboard(getRoomUrl(state.currentRoom.roomCode), '房间链接已复制');
  }

  function copyMessage(messageId) {
    const message = state.messages.find(item => Number(item.id) === Number(messageId));
    if (!message || message.type === 'file') return;
    copyToClipboard(message.textContent || '', '文本已复制');
  }

  function startLifecycleTimer() {
    if (lifecycleTimer) clearInterval(lifecycleTimer);
    lifecycleTimer = setInterval(() => {
      if (!state.currentRoom || !state.currentRoom.deleteAfter || state.currentRoom.isPermanent) {
        clearInterval(lifecycleTimer);
        lifecycleTimer = null;
        return;
      }
      const remaining = S.formatRemaining(state.currentRoom.deleteAfter);
      const el = document.getElementById('lifecycle-display');
      if (el && remaining) {
        el.textContent = remaining.text;
        el.className = 'lifecycle-value ' + remaining.cls + (remaining.sec < 3600 ? ' countdown-warning' : '');
      }
      if (remaining && remaining.sec <= 0) {
        clearInterval(lifecycleTimer);
        lifecycleTimer = null;
        leaveCurrentRoom();
        setHash('');
        render();
        banner('error', '房间已过期并被销毁');
      }
    }, 10000);
  }

  async function initialize() {
    state.loading = true;
    applyTheme(loadThemePreference());
    applyAccent(loadAccentPreference());
    render();
    try {
      const [session, publicSettings] = await Promise.all([
        api.getSession(),
        api.getPublicSettings().catch(() => null),
      ]);
      state.authenticated = !!session.authenticated;
      if (publicSettings) {
        state.publicSettings = publicSettings;
        if (publicSettings.maxUploadBytes) state.uploadLimitBytes = publicSettings.maxUploadBytes;
        if (publicSettings.maxMessageTextLength) state.maxMessageTextLength = publicSettings.maxMessageTextLength;
      }
      if (state.roomCode) {
        await enterRoom(state.roomCode, { syncHash: false });
      }
    } catch (_) {
      // ignore init errors
    } finally {
      state.loading = false;
      render();
    }
  }

  async function createRoom() {
    try {
      const room = await api.createRoom();
      state.workspaceTransition = 'entering-room';
      render();
      const ok = await enterRoom(room.roomCode, { syncHash: true });
      if (!ok) {
        state.workspaceTransition = '';
        return;
      }
      banner('success', `房间 ${room.roomCode} 已创建`);
      setTimeout(() => {
        state.workspaceTransition = '';
        updateViewState();
      }, 700);
    } catch (e) {
      state.workspaceTransition = '';
      banner('error', e.message);
    }
  }

  async function joinRoom() {
    const code = state.roomCode.trim().toUpperCase();
    if (!code) {
      banner('error', '请输入房间号');
      return;
    }
    // 先检查房间是否存在，避免先播放动画再报错
    try {
      await api.getRoom(code);
    } catch (e) {
      if (/不存在/.test(e.message) || /not found/i.test(e.message)) removeRecentRoom(code);
      banner('error', '房间不存在或已关闭');
      return;
    }
    state.workspaceTransition = 'entering-room';
    render();
    const ok = await enterRoom(code, { syncHash: true });
    if (!ok) state.workspaceTransition = '';
    else setTimeout(() => {
      state.workspaceTransition = '';
      updateViewState();
    }, 700);
  }

  async function enterRoom(code, options) {
    const opts = options || {};
    const normalized = (code || '').trim().toUpperCase();
    if (!normalized) return false;

    const token = ++activeEnterToken;
    setConnectionStatus('connecting');

    try {
      const room = await api.getRoom(normalized);
      if (token !== activeEnterToken) return false;

      disconnectRealtime();
      state.currentRoom = room;
      state.currentRoom.roomCode = room.roomCode;
      if (room.maxUploadBytes) state.uploadLimitBytes = room.maxUploadBytes;
      if (room.maxMessageTextLength) state.maxMessageTextLength = room.maxMessageTextLength;
      state.roomCode = room.roomCode;
      state.messages = [];
      state.lastEventId = 0;
      state.filesToUpload = [];
      rememberRecentRoom(room);
      if (opts.syncHash !== false) setHash(room.roomCode);
      render();

      await refreshMessages(token);
      if (token !== activeEnterToken) return false;

      state.workspaceTransition = '';
      connectRealtime();
      startLifecycleTimer();
      closeRecentRoomsDrawer();
      if (typeof opts.afterEnter === 'function') opts.afterEnter(room);
      return true;
    } catch (e) {
      if (token !== activeEnterToken) return false;
      leaveCurrentRoom();
      if (/不存在/.test(e.message) || /not found/i.test(e.message)) removeRecentRoom(normalized);
      if (opts.syncHash !== false) setHash('');
      render();
      banner('error', '房间不存在或已关闭');
      return false;
    }
  }

  async function refreshMessages(token) {
    if (!state.currentRoom) return;
    try {
      const data = await api.listMessages(state.currentRoom.roomCode);
      if (token && token !== activeEnterToken) return;
      state.messages = sortMessages(data.messages || []);
      updateMessages();
    } catch (_) {
      // silent refresh failures
    }
  }

  async function sendMessage() {
    if (sendInFlight) return;
    if (!state.currentRoom) return;
    const text = state.draft.trim();
    if (!text) {
      banner('error', '请输入消息内容');
      return;
    }
    if ([...text].length > state.maxMessageTextLength) {
      banner('error', `文字消息超过当前限制：${state.maxMessageTextLength} 个字符`);
      return;
    }
    sendInFlight = true;
    try {
      const data = await api.sendText(state.currentRoom.roomCode, text);
      state.messages = sortMessages([data.message, ...state.messages.filter(m => m.id !== data.message.id)]);
      state.lastEventId = Math.max(state.lastEventId, data.event?.id || 0);
      state.draft = '';
      updateMessages();
      updateSidebar();
    } catch (e) {
      banner('error', e.message);
    } finally {
      sendInFlight = false;
    }
  }

  async function uploadFile() {
    if (uploadInFlight) return;
    if (!state.currentRoom) return;
    const fileQueue = state.filesToUpload || [];
    if (fileQueue.length === 0) {
      banner('error', '请选择文件');
      return;
    }
    const oversize = fileQueue.find(file => file.size > state.uploadLimitBytes);
    if (oversize) {
      banner('error', `文件 ${oversize.name} 超过当前限制：${formatSize(state.uploadLimitBytes)}`);
      return;
    }
    uploadInFlight = true;
    try {
      for (let i = 0; i < fileQueue.length; i++) {
        const file = fileQueue[i];
        state.uploadStartedAt = Date.now();
        state.uploadTransferredBytes = 0;
        state.uploadProgressPct = 0;
        state.uploadSpeedBps = 0;
        updateSidebar();
        const data = await api.uploadFile(state.currentRoom.roomCode, file, {
          onProgress(loaded, total) {
            state.uploadTransferredBytes = loaded;
            state.uploadProgressPct = total ? Math.round((loaded / total) * 100) : 0;
            const elapsed = (Date.now() - state.uploadStartedAt) / 1000;
            state.uploadSpeedBps = elapsed > 0 ? loaded / elapsed : 0;
            updateSidebar();
          },
        });
        state.messages = sortMessages([data.message, ...state.messages.filter(m => m.id !== data.message.id)]);
        state.lastEventId = Math.max(state.lastEventId, data.event?.id || 0);
      }
      banner('success', `已上传 ${fileQueue.length} 个文件`);
      uploadInFlight = false;
      state.filesToUpload = [];
      state.uploadTransferredBytes = 0;
      state.uploadProgressPct = 0;
      state.uploadSpeedBps = 0;
      updateMessages();
      updateSidebar();
    } catch (e) {
      uploadInFlight = false;
      state.uploadTransferredBytes = 0;
      state.uploadProgressPct = 0;
      state.uploadSpeedBps = 0;
      updateSidebar();
      banner('error', e.message);
    } finally { }
  }

  async function extendRoom() {
    if (!state.currentRoom) return;
    try {
      const room = await api.extendRoom(state.currentRoom.roomCode);
      state.currentRoom = room;
      banner('success', '房间已续期 24 小时');
      updateSidebar();
    } catch (e) {
      banner('error', e.message);
    }
  }

  async function setPermanentRoom() {
    if (!state.currentRoom) return;
    try {
      const room = await api.setPermanentRoom(state.currentRoom.roomCode);
      state.currentRoom = room;
      banner('success', '房间已设为长期房间');
      updateSidebar();
    } catch (e) {
      banner('error', e.message);
    }
  }

  async function destroyRoom() {
    if (!state.currentRoom) return;
    try {
      const code = state.currentRoom.roomCode;
      await api.deleteRoom(code);
      removeRecentRoom(code);
      leaveCurrentRoom();
      setHash('');
      render();
      banner('notice', `房间 ${code} 已销毁`);
    } catch (e) {
      banner('error', e.message);
    }
  }

  async function togglePin(msgId) {
    if (messageActionInFlight.has(`pin:${msgId}`)) return;
    messageActionInFlight.add(`pin:${msgId}`);
    try {
      const result = await api.togglePin(msgId);
      const msg = result.message;
      state.messages = sortMessages(state.messages.map(m => m.id === msgId ? msg : m));
      state.lastEventId = Math.max(state.lastEventId, result?.event?.id || 0);
      updateMessages();
    } catch (e) {
      banner('error', e.message);
    } finally {
      messageActionInFlight.delete(`pin:${msgId}`);
    }
  }

  async function deleteMessage(msgId) {
    if (messageActionInFlight.has(`delete:${msgId}`)) return;
    messageActionInFlight.add(`delete:${msgId}`);
    try {
      const result = await api.deleteMessage(msgId);
      state.messages = state.messages.filter(m => m.id !== msgId);
      state.lastEventId = Math.max(state.lastEventId, result?.event?.id || 0);
      updateMessages();
    } catch (e) {
      banner('error', e.message);
    } finally {
      messageActionInFlight.delete(`delete:${msgId}`);
    }
  }

  async function adminLogin() {
    const pw = document.getElementById('admin-password')?.value || '';
    if (!pw.trim()) {
      banner('error', '请输入密码');
      return;
    }
    try {
      const data = await api.login(pw.trim());
      state.authenticated = !!data.authenticated;
      if (state.authenticated) {
        state.adminTab = 'rooms';
        await Promise.all([loadAdminRooms(), loadAdminSettings()]);
        banner('success', '管理员登录成功');
      }
      updateAdminView();
    } catch (e) {
      banner('error', e.message);
    }
  }

  async function adminLogout() {
    try { await api.logout(); } catch (_) { }
    state.authenticated = false;
    state.adminPasswordFeedback = '';
    state.adminPasswordFeedbackType = '';
    state.adminRooms = [];
    state.adminTab = 'rooms';
    state.adminSettings = null;
    state.adminNewPassword = '';
    state.adminConfirmPassword = '';
    updateAdminView();
    banner('notice', '已退出管理后台');
  }

  async function loadAdminRooms() {
    if (!state.authenticated) return;
    try {
      const data = await api.listAdminRooms(state.adminPage, state.adminPageSize);
      state.adminRooms = data.rooms || [];
      state.adminTotalRooms = data.total || 0;
      state.adminPage = data.page || 1;
      state.adminPageSize = data.pageSize || 10;
      state.adminStats = data.stats || null;
      updateAdminView();
    } catch (_) {
      // silent
    }
  }

  async function adminDeleteRoom(code) {
    try {
      await api.adminDeleteRoom(code);
      removeRecentRoom(code);
      if (state.currentRoom && state.currentRoom.roomCode === code) {
        leaveCurrentRoom();
        setHash('');
      }
      await loadAdminRooms();
      render();
      banner('notice', `房间 ${code} 已删除`);
    } catch (e) {
      banner('error', e.message);
    }
  }

  function handleAdminBatchDelete() {
    const checked = [...document.querySelectorAll('.admin-room-check:checked')];
    const codes = checked.map(cb => cb.dataset.roomCode);
    if (codes.length === 0) {
      banner('error', '请先勾选要删除的房间');
      return;
    }
    showConfirm('批量删除房间', `确认删除选中的 ${codes.length} 个房间？此操作不可撤销。`, async () => {
      try {
        await api.adminBatchDeleteRooms(codes);
        for (const code of codes) {
          removeRecentRoom(code);
          if (state.currentRoom && state.currentRoom.roomCode === code) {
            leaveCurrentRoom();
            setHash('');
          }
        }
        await loadAdminRooms();
        render();
        banner('notice', `已删除 ${codes.length} 个房间`);
      } catch (e) {
        banner('error', e.message);
      }
    });
  }

  // 监听 checkbox 变化，显示/隐藏批量删除按钮，以及全选
  function attachAdminCheckListeners() {
    const selectAll = document.getElementById('admin-select-all');
    const checks = document.querySelectorAll('.admin-room-check');

    function updateBatchBtn() {
      const btn = document.getElementById('admin-batch-delete-btn');
      if (btn) {
        const anyChecked = [...document.querySelectorAll('.admin-room-check:checked')].length > 0;
        btn.style.display = anyChecked ? '' : 'none';
      }
      // 同步全选框状态
      if (selectAll) {
        const allChecked = checks.length > 0 && [...checks].every(cb => cb.checked);
        const noneChecked = [...checks].every(cb => !cb.checked);
        selectAll.checked = allChecked;
        selectAll.indeterminate = !allChecked && !noneChecked;
      }
    }

    if (selectAll) {
      selectAll.addEventListener('change', () => {
        const checked = selectAll.checked;
        for (const cb of checks) {
          cb.checked = checked;
        }
        updateBatchBtn();
      });
    }

    for (const cb of checks) {
      cb.addEventListener('change', updateBatchBtn);
    }

    // 每页数量切换
    const pageSizeSelect = document.getElementById('admin-page-size-select');
    if (pageSizeSelect) {
      pageSizeSelect.addEventListener('change', () => {
        state.adminPageSize = parseInt(pageSizeSelect.value, 10) || 10;
        state.adminPage = 1;
        loadAdminRooms();
      });
    }
  }

  async function loadAdminSettings() {
    if (!state.authenticated) return;
    try {
      const data = await api.adminGetSettings();
      state.adminSettings = normalizeAdminSettings(data);
      if (data.maxUploadBytes) state.uploadLimitBytes = data.maxUploadBytes;
      if (data.maxMessageTextLength) state.maxMessageTextLength = data.maxMessageTextLength;
      updateAdminView();
    } catch (_) {
      // silent
    }
  }

  async function saveAdminSettings() {
    const inputs = document.querySelectorAll('.settings-input, .settings-textarea');
    const settings = {};
    inputs.forEach(el => {
      const key = el.dataset.settingKey;
      if (!key) return;
      if (DURATION_SETTING_KEYS.has(key)) settings[key] = parseFloat(el.value) || 0;
      else if (el.tagName === 'TEXTAREA' || el.type === 'text') settings[key] = el.value;
      else settings[key] = parseInt(el.value, 10) || 0;
    });
    // 布尔值设置
    const checks = document.querySelectorAll('.settings-checkbox');
    checks.forEach(el => {
      const key = el.dataset.settingKey;
      if (key) settings[key] = el.checked;
    });
    const payload = denormalizeAdminSettings(settings);
    try {
      await api.adminUpdateSettings(payload);
      state.adminSettings = normalizeAdminSettings(payload);
      state.publicSettings = {
        emptyStateTitle: state.adminSettings.emptyStateTitle,
        emptyStateBody: state.adminSettings.emptyStateBody,
      };
      state.uploadLimitBytes = payload.maxUploadBytes;
      state.maxMessageTextLength = payload.maxMessageTextLength;
      updateAdminView();
      updateSidebar();
      updateMessages();
      banner('success', '设置已保存');
    } catch (e) {
      banner('error', e.message);
    }
  }

  async function saveRoomName() {
    if (!state.currentRoom) return;
    const input = document.getElementById('room-name-input');
    const roomName = input?.value || '';
    try {
      const room = await api.updateRoomName(state.currentRoom.roomCode, roomName);
      state.currentRoom = { ...state.currentRoom, ...room };
      state.roomNameEditing = false;
      rememberRecentRoom(state.currentRoom);
      updateSidebar();
      updateRecentRoomsDrawer();
      banner('success', '房间名称已保存');
    } catch (e) {
      banner('error', e.message);
    }
  }

  function toggleEditRoomName() {
    state.roomNameEditing = !state.roomNameEditing;
    updateSidebar();
  }

  function cancelEditRoomName() {
    state.roomNameEditing = false;
    updateSidebar();
  }

  function removeUploadFile(index) {
    if (!Array.isArray(state.filesToUpload)) return;
    state.filesToUpload = state.filesToUpload.filter((_, i) => i !== index);
    updateSidebar();
  }

  function clearUploadFiles() {
    state.filesToUpload = [];
    updateSidebar();
  }

  async function changeAdminPassword() {
    const next = state.adminNewPassword.trim();
    const confirm = state.adminConfirmPassword.trim();
    if (!next || !confirm) {
      state.adminPasswordFeedback = '请输入两次新密码';
      state.adminPasswordFeedbackType = 'error';
      updateAdminView();
      banner('error', '请输入两次新密码');
      return;
    }
    if (next !== confirm) {
      state.adminPasswordFeedback = '两次输入的新密码不一致';
      state.adminPasswordFeedbackType = 'error';
      updateAdminView();
      banner('error', '两次输入的新密码不一致');
      return;
    }
    try {
      await api.adminChangePassword(next, confirm);
      state.adminNewPassword = '';
      state.adminConfirmPassword = '';
      state.adminPasswordFeedback = '管理员密码已更新';
      state.adminPasswordFeedbackType = 'success';
      updateAdminView();
      banner('success', '管理员密码已更新');
    } catch (e) {
      state.adminPasswordFeedback = e.message;
      state.adminPasswordFeedbackType = 'error';
      updateAdminView();
      banner('error', e.message);
    }
  }

  async function joinRecentRoom(code) {
    if (!code) return;
    state.roomCode = code;
    state.workspaceTransition = 'entering-room';
    render();
    const ok = await enterRoom(code, { syncHash: true });
    if (!ok) state.workspaceTransition = '';
    else setTimeout(() => {
      state.workspaceTransition = '';
      updateViewState();
    }, 700);
  }

  function connectRealtime() {
    realtime.connect({
      get currentRoom() { return state.currentRoom; },
      get connectionStatus() { return state.connectionStatus; },
      get lastEventId() { return state.lastEventId; },
      set lastEventId(v) { state.lastEventId = v; },
      get ws() { return ws; }, set ws(v) { ws = v; },
      get pollTimer() { return pollTimer; }, set pollTimer(v) { pollTimer = v; },
      get reconnectTimer() { return reconnectTimer; }, set reconnectTimer(v) { reconnectTimer = v; },
      get pollTimeout() { return pollTimer; }, set pollTimeout(v) { pollTimer = v; },
      get reconnectTimeout() { return reconnectTimer; }, set reconnectTimeout(v) { reconnectTimer = v; },
      get pollAttempt() { return state.pollAttempt || 0; }, set pollAttempt(v) { state.pollAttempt = v; },
      get reconnectAttempt() { return state.reconnectAttempt || 0; }, set reconnectAttempt(v) { state.reconnectAttempt = v; },
      get pollInFlight() { return !!state.pollInFlight; }, set pollInFlight(v) { state.pollInFlight = v; },
      setConnectionStatus,
      banner: (msg) => { if (msg) banner('error', msg); },
      async refreshMessages() { await refreshMessages(); },
      removeMessage(messageId) {
        state.messages = state.messages.filter(m => m.id !== messageId);
        updateMessages();
      },
      applyRoomEvent(event) {
        if (!state.currentRoom || event.roomCode !== state.currentRoom.roomCode) return;
        if (event.type === 'room_deleted') {
          leaveCurrentRoom();
          setHash('');
          removeRecentRoom(event.roomCode);
          render();
          banner('notice', '当前房间已被删除');
          return;
        }
        if (event.room) {
          state.currentRoom = event.room;
          updateSidebar();
        }
      },
      async applyMessageEvent(event) {
        if (!event?.messageId) {
          await refreshMessages();
          return;
        }
        try {
          const data = await api.listMessages(state.currentRoom.roomCode);
          const latest = (data.messages || []).find(m => m.id === event.messageId);
          if (latest) {
            state.messages = sortMessages([latest, ...state.messages.filter(m => m.id !== latest.id)]);
            updateMessages();
            return;
          }
          await refreshMessages();
        } catch (_) {
          await refreshMessages();
        }
      },
      upsertMessage(message) {
        state.messages = sortMessages([message, ...state.messages.filter(m => m.id !== message.id)]);
        updateMessages();
      },
    });
  }

  function disconnectRealtime() {
    realtime.disconnect({
      get ws() { return ws; }, set ws(v) { ws = v; },
      get pollTimeout() { return pollTimer; }, set pollTimeout(v) { pollTimer = v; },
      get reconnectTimeout() { return reconnectTimer; }, set reconnectTimeout(v) { reconnectTimer = v; },
      get pollAttempt() { return state.pollAttempt || 0; }, set pollAttempt(v) { state.pollAttempt = v; },
      get reconnectAttempt() { return state.reconnectAttempt || 0; }, set reconnectAttempt(v) { state.reconnectAttempt = v; },
      get pollInFlight() { return !!state.pollInFlight; }, set pollInFlight(v) { state.pollInFlight = v; },
    });
    setConnectionStatus('offline');
  }

  state.recentRooms = loadRecentRooms();
  initialize();
})();
