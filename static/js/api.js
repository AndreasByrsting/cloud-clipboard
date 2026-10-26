const API_STATUS_MESSAGES = {
  400: '请求内容格式不正确',
  401: '登录状态已失效，请重新登录',
  403: '没有权限执行该操作',
  404: '请求的资源不存在',
  405: '当前操作不被允许',
  413: '内容超过大小限制',
  429: '操作太频繁，请稍后再试',
  500: '服务器内部错误，请稍后重试',
  502: '服务暂时不可用，请稍后重试',
  503: '服务暂时不可用，请稍后重试',
  504: '服务响应超时，请稍后重试',
};

const API_MESSAGE_ALIASES = {
  'request failed': '请求失败，请稍后重试',
  'network error': '网络连接失败，请检查网络后重试',
  'failed to fetch': '网络连接失败，请检查网络后重试',
  'load failed': '网络连接失败，请检查网络后重试',
  'not found': '请求的资源不存在',
  'unauthorized': '登录状态已失效，请重新登录',
  'forbidden': '没有权限执行该操作',
  'internal server error': '服务器内部错误，请稍后重试',
  'bad gateway': '服务暂时不可用，请稍后重试',
  'service unavailable': '服务暂时不可用，请稍后重试',
  'gateway timeout': '服务响应超时，请稍后重试',
};

function localizeApiError(message, code, status) {
  if (code === 'password_change_required') return '请先修改初始管理员密码';
  if (code === 'rate_limited') return '操作太频繁，请稍后再试';

  const text = typeof message === 'string' ? message.trim() : '';
  // 后端已经返回中文时原样展示，避免覆盖具体原因。
  if (text && /[\u3400-\u9fff]/.test(text)) return text;
  if (text) {
    const alias = API_MESSAGE_ALIASES[text.toLowerCase()];
    if (alias) return alias;
  }
  if (status && API_STATUS_MESSAGES[status]) return API_STATUS_MESSAGES[status];
  return '操作失败，请稍后重试';
}

window.CloudClipboardApi = {
  async request(config) {
    let response;
    try {
      response = await fetch(config.url, {
        method: config.method || 'GET',
        headers: {
          ...(config.body instanceof FormData ? {} : { 'Content-Type': 'application/json' }),
          ...(config.headers || {}),
        },
        body: config.body instanceof FormData ? config.body : config.body ? JSON.stringify(config.body) : undefined,
        credentials: 'same-origin',
      });
    } catch (_) {
      throw new Error('网络连接失败，请检查网络后重试');
    }

    let data = null;
    const contentType = response.headers.get('content-type') || '';
    if (contentType.includes('application/json')) {
      try {
        data = await response.json();
      } catch (_) {
        data = null;
      }
    }
    if (!response.ok) {
      const err = new Error(localizeApiError(data?.error, data?.code, response.status));
      if (data && data.code !== undefined) err.code = data.code;
      throw err;
    }
    return data;
  },

  // Room
  createRoom() { return this.request({ method: 'POST', url: '/api/rooms' }); },
  getRoom(roomCode) { return this.request({ url: `/api/rooms/${roomCode}/detail` }); },
  updateRoomName(roomCode, roomName) { return this.request({ method: 'POST', url: `/api/rooms/${roomCode}/name`, body: { roomName } }); },
  extendRoom(roomCode) { return this.request({ method: 'POST', url: `/api/rooms/${roomCode}/extend` }); },
  setPermanentRoom(roomCode) { return this.request({ method: 'POST', url: `/api/rooms/${roomCode}/permanent` }); },
  deleteRoom(roomCode) { return this.request({ method: 'DELETE', url: `/api/rooms/${roomCode}` }); },

  // Messages
  listMessages(roomCode) { return this.request({ url: `/api/rooms/${roomCode}/messages` }); },
  sendText(roomCode, text) { return this.request({ method: 'POST', url: `/api/rooms/${roomCode}/messages`, body: { text } }); },
  async createUploadSession(roomCode, file) {
    return this.request({ method: 'POST', url: `/api/rooms/${roomCode}/uploads`, body: { fileName: file.name, fileSize: file.size, mimeType: file.type || 'application/octet-stream' } });
  },
  uploadChunk(roomCode, uploadId, chunk) {
    return new Promise((resolve, reject) => {
      const xhr = new XMLHttpRequest();
      xhr.open('PUT', `/api/rooms/${roomCode}/uploads/${uploadId}/chunk`, true);
      xhr.withCredentials = true;
      xhr.onreadystatechange = () => {
        if (xhr.readyState !== 4) return;
        let data = null;
        try { data = xhr.responseText ? JSON.parse(xhr.responseText) : null; } catch (_) {}
        if (xhr.status >= 200 && xhr.status < 300) {
          resolve(data);
        } else {
          const err = new Error(localizeApiError(data?.error, data?.code, xhr.status));
          if (data && data.code !== undefined) err.code = data.code;
          reject(err);
        }
      };
      xhr.onerror = () => reject(new Error('网络连接失败，请检查网络后重试'));
      xhr.send(chunk);
    });
  },
  finalizeUpload(roomCode, uploadId) {
    return this.request({ method: 'POST', url: `/api/rooms/${roomCode}/uploads/${uploadId}/complete` });
  },
  uploadFile(roomCode, file, callbacks) {
    return new Promise(async (resolve, reject) => {
      try {
        const session = await this.createUploadSession(roomCode, file);
        const chunkSize = session.chunkSize || 5 * 1024 * 1024;
        let uploaded = 0;
        for (let offset = 0; offset < file.size; offset += chunkSize) {
          const chunk = file.slice(offset, offset + chunkSize);
          await this.uploadChunk(roomCode, session.id, chunk);
          uploaded += chunk.size;
          if (callbacks?.onProgress) callbacks.onProgress(uploaded, file.size);
        }
        const data = await this.finalizeUpload(roomCode, session.id);
        resolve(data);
      } catch (err) {
        reject(err);
      }
    });
  },
  listEvents(roomCode, sinceEventId) {
    const p = new URLSearchParams(); if (sinceEventId) p.set('since_event_id', String(sinceEventId));
    return this.request({ url: `/api/rooms/${roomCode}/events?${p.toString()}` });
  },

  // Message actions
  deleteMessage(roomCode, id) { return this.request({ method: 'DELETE', url: `/api/rooms/${roomCode}/messages/${id}` }); },
  togglePin(roomCode, id) { return this.request({ method: 'POST', url: `/api/rooms/${roomCode}/messages/${id}/pin` }); },
  downloadFile(roomCode, messageId, inline = false) {
    const path = `/api/rooms/${roomCode}/messages/${messageId}/file`;
    return inline ? `${path}?inline=1` : path;
  },

  // Public bootstrap
  bootstrapStatus() { return this.request({ url: '/api/bootstrap/status' }); },
  getPublicSettings() { return this.request({ url: '/api/bootstrap/settings' }); },

  // Admin
  getSession() { return this.request({ url: '/api/admin/session' }); },
  login(password) { return this.request({ method: 'POST', url: '/api/admin/login', body: { password } }); },
  logout() { return this.request({ method: 'POST', url: '/api/admin/logout' }); },
  adminChangePassword(currentPassword, newPassword, confirmPassword) {
    return this.request({ method: 'POST', url: '/api/admin/password', body: { currentPassword, newPassword, confirmPassword } });
  },
  listAdminRooms(page, pageSize) {
    const p = new URLSearchParams();
    if (page) p.set('page', String(page));
    if (pageSize) p.set('pageSize', String(pageSize));
    const qs = p.toString();
    return this.request({ url: `/api/admin/rooms${qs ? '?' + qs : ''}` });
  },
  adminDeleteRoom(roomCode) { return this.request({ method: 'DELETE', url: `/api/admin/rooms/${roomCode}/delete` }); },
  adminBatchDeleteRooms(roomCodes) { return this.request({ method: 'DELETE', url: '/api/admin/rooms', body: { roomCodes } }); },
  adminGetSettings() { return this.request({ url: '/api/admin/settings' }); },
  adminUpdateSettings(settings) { return this.request({ method: 'PUT', url: '/api/admin/settings', body: settings }); },
};
