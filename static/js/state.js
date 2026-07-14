window.CloudClipboardState = {
  createInitialState() {
    const hash = window.location.hash;
    const hashRoom = hash.startsWith('#') ? hash.slice(1) : '';
    return {
      loading: true,
      roomCode: hashRoom || '',
      currentRoom: null,
      messages: [],
      draft: '',
      filesToUpload: [],
      confirmDialog: null,
      lastEventId: 0,
      connectionStatus: 'offline',
      connectionLabel: '离线',
      error: '',
      success: '',
      notice: '',
      publicSettings: {
        emptyStateTitle: '选择一个房间开始',
        emptyStateBody: '•尝试创建新房间或通过房间号加入已有房间，即可开始同步内容\n•您的数据将在服务器上有限期存储，除非您手动设置为永久房间\n•本服务仅作为数据传输工具，请勿依赖本服务作为唯一存储介质\n•房间隔离码不足以构成加密保护，请勿传输未加密重要敏感信息\n•您需对传输内容的合法性自行承担全部责任，禁止传播非法内容',
        roomCanExtendSec: 12 * 3600,
        roomExtendSec: 24 * 3600,
      },
      uploadLimitBytes: 500 * 1024 * 1024,
      maxMessageTextLength: 40960,
      theme: 'dark',
      accent: 'sky',
      accentPickerOpen: false,
      roomNameEditing: false,
      expandedMessageIds: {},
      uploadProgressPct: 0,
      uploadTransferredBytes: 0,
      uploadSpeedBps: 0,
      uploadStartedAt: 0,
      // admin
      authenticated: false,
      activeView: 'workspace',
      adminPassword: '',
      adminNewPassword: '',
      adminConfirmPassword: '',
      adminPasswordFeedback: '',
      adminPasswordFeedbackType: '',
      adminRooms: [],
      adminTab: 'rooms',
      adminPage: 1,
      adminPageSize: 10,
      adminTotalRooms: 0,
      adminStats: null,
      adminSettings: null,
      mobileQRVisible: false,
      mobileRoomCardVisible: false,
      mobileFabExpanded: false,
      workspaceTransition: '',
      recentRooms: [],
      recentRoomsDrawerOpen: false,
      showScrollTop: false,
      // lifecycle timer
      lifecycleTimer: null,
      origin: window.location.origin,
    };
  },

  statusTone(status) {
    switch (status) {
      case 'websocket': return 'success';
      case 'polling': return 'warning';
      case 'connecting': return 'brand';
      case 'error': return 'danger';
      default: return 'muted';
    }
  },

  statusLabel(status) {
    switch (status) {
      case 'websocket': return '即时连接';
      case 'polling': return '轮询连接';
      case 'connecting': return '连接中';
      case 'error': return '连接异常';
      default: return '未连接';
    }
  },

  formatDate(unixSeconds) {
    if (!unixSeconds) return '—';
    return new Date(unixSeconds * 1000).toLocaleString('zh-CN', {
      hour12: false, year: 'numeric', month: '2-digit', day: '2-digit',
      hour: '2-digit', minute: '2-digit', second: '2-digit',
    });
  },

  formatDateTime(unixSeconds) {
    if (!unixSeconds) return '—';
    const d = new Date(unixSeconds * 1000);
    const pad = n => String(n).padStart(2, '0');
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
  },

  escapeHTML(value) {
    return String(value ?? '')
      .replace(/&/g, '&amp;').replace(/</g, '&lt;')
      .replace(/>/g, '&gt;').replace(/"/g, '&quot;').replace(/'/g, '&#39;');
  },

  formatRemaining(deleteAfter) {
    if (!deleteAfter) return null;
    const now = Math.floor(Date.now() / 1000);
    const remaining = deleteAfter - now;
    if (remaining <= 0) return { text: '已过期', cls: 'danger', sec: 0 };
    if (remaining < 60) return { text: '不足1分钟', cls: 'danger', sec: remaining };
    const h = Math.floor(remaining / 3600);
    const m = Math.floor((remaining % 3600) / 60);
    if (h >= 24) {
      const d = Math.floor(h / 24);
      return { text: `约 ${d} 天 ${h % 24} 小时`, cls: '', sec: remaining };
    }
    if (h >= 12) return { text: `${h} 小时 ${m} 分钟`, cls: '', sec: remaining };
    if (h >= 1) return { text: `${h} 小时 ${m} 分钟`, cls: 'warning', sec: remaining };
    return { text: `${m} 分钟`, cls: 'danger', sec: remaining };
  },

  canExtend(remaining, thresholdSec) {
    return remaining !== null && remaining > 0 && remaining <= thresholdSec;
  },
};
