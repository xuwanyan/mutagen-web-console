import axios from 'axios'

const api = axios.create({
  baseURL: '/api',
  timeout: 10000
})

// 自动在请求头添加登录 token
api.interceptors.request.use(config => {
  const token = localStorage.getItem('auth_token')
  if (token) {
    config.headers.Authorization = `Bearer ${token}`
  }
  return config
})

// 处理 401 跳转登录（只跳一次，避免循环）
let authRedirecting = false
api.interceptors.response.use(
  r => r,
  e => {
    if (e.response && e.response.status === 401 && !authRedirecting) {
      authRedirecting = true
      localStorage.removeItem('auth_token')
      setTimeout(() => { window.location.reload() }, 100)
      // 3s 后重置，防止 reload 被浏览器阻止时后续 401 静默吞掉
      setTimeout(() => { authRedirecting = false }, 3000)
    }
    return Promise.reject(e)
  }
)

export const authApi = {
  login: (username, password) => api.post('/login', { username, password })
}

export const machineApi = {
  list: () => api.get('/machines'),
  create: (name) => api.post('/machines', { name }),
  get: (id) => api.get(`/machines/${id}`),
  delete: (id) => api.delete(`/machines/${id}`),
  regenerateToken: (id) => api.post(`/machines/${id}/regenerate-token`),
  testConnection: (id) => api.post(`/machines/${id}/test-connection`),
  downloadPack: (id) => api.get(`/machines/${id}/agent-pack`, { responseType: 'blob', timeout: 120000 })
}

export const taskApi = {
  list: (machineId) => api.get(`/machines/${machineId}/tasks`),
  create: (machineId, data) => api.post(`/machines/${machineId}/tasks`, data),
  pauseAll: (machineId) => api.post(`/machines/${machineId}/tasks/pause-all`),
  resumeAll: (machineId) => api.post(`/machines/${machineId}/tasks/resume-all`),
  terminateAll: (machineId) => api.post(`/machines/${machineId}/tasks/terminate-all`),
  pause: (machineId, taskId) => api.post(`/machines/${machineId}/tasks/${taskId}/pause`),
  resume: (machineId, taskId) => api.post(`/machines/${machineId}/tasks/${taskId}/resume`),
  terminate: (machineId, taskId) => api.delete(`/machines/${machineId}/tasks/${taskId}`),
  refreshStatus: (machineId) => api.post(`/machines/${machineId}/refresh-status`),
  refreshRemoteAgents: (machineId) => api.post(`/machines/${machineId}/refresh-remote-agents`, {}, { timeout: 70000 }),
  retry: (machineId, taskId) => api.post(`/machines/${machineId}/tasks/${taskId}/retry`),
  update: (machineId, taskId, data) => api.put(`/machines/${machineId}/tasks/${taskId}`, data),
  backupData: () => api.get('/backup-data', { responseType: 'blob', timeout: 30000 }),
  getCommandTemplate: (machineId) => api.get(`/machines/${machineId}/command-template`)
}

export const configApi = {
  getGlobal: (machineId) => api.get(`/machines/${machineId}/config/global`),
  updateGlobal: (machineId, content) => api.put(`/machines/${machineId}/config/global`, { content }),
  getSSH: (machineId) => api.get(`/machines/${machineId}/config/ssh`),
  updateSSH: (machineId, content) => api.put(`/machines/${machineId}/config/ssh`, { content }),
  getSSHHosts: (machineId) => api.get(`/machines/${machineId}/config/ssh-hosts`),
  updateSSHHosts: (machineId, hosts) => api.put(`/machines/${machineId}/config/ssh-hosts`, { hosts }),
  importSSHHosts: (machineId) => api.post(`/machines/${machineId}/config/ssh-hosts/import`, {}, { timeout: 40000 }),
  getBackup: (machineId) => api.get(`/machines/${machineId}/config/backup`),
  updateBackup: (machineId, data) => api.put(`/machines/${machineId}/config/backup`, data),
  verifyBackup: (machineId) => api.post(`/machines/${machineId}/config/backup/verify`, {}, { timeout: 40000 }),
  getBackupLinux: (machineId) => api.get(`/machines/${machineId}/config/backup-linux`),
  updateBackupLinux: (machineId, data) => api.put(`/machines/${machineId}/config/backup-linux`, data, { timeout: 40000 }),
  verifyBackupLinux: (machineId) => api.post(`/machines/${machineId}/config/backup-linux/verify`, {}, { timeout: 40000 })
}
