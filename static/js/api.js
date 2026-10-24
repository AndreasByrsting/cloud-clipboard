window.CloudClipboardApi = {
  async request(config) {
    const response = await fetch(config.url, {
      method: config.method || 'GET',
      headers: {
        ...(config.body instanceof FormData ? {} : { 'Content-Type': 'application/json' }),
        ...(config.headers || {}),
      },
      body: config.body instanceof FormData ? config.body : config.body ? JSON.stringify(config.body) : undefined,
      credentials: 'same-origin',
    });
    let data = null;
    const contentType = response.headers.get('content-type') || '';
    if (contentType.includes('application/json')) {
      data = await response.json();
    }
    if (!response.ok) {
      const err = new Error(data?.error || response.statusText || 'request failed');
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
          const err = new Error(data?.error || xhr.statusText || 'request failed');
          if (data && data.code !== undefined) err.code = data.code;
          reject(err);
        }
      };
      xhr.onerror = () => reject(new Error('network error'));
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
