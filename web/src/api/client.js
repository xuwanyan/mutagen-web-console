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
  pause: (machineId, taskId) => api.post(`/machines/${machineId}/tasks/${taskId}/pause`),
  resume: (machineId, taskId) => api.post(`/machines/${machineId}/tasks/${taskId}/resume`),
  terminate: (machineId, taskId) => api.delete(`/machines/${machineId}/tasks/${taskId}`),
  refreshStatus: (machineId) => api.post(`/machines/${machineId}/refresh-status`),
  retry: (machineId, taskId) => api.post(`/machines/${machineId}/tasks/${taskId}/retry`),
  update: (machineId, taskId, data) => api.put(`/machines/${machineId}/tasks/${taskId}`, data)
}

export const configApi = {
  getGlobal: (machineId) => api.get(`/machines/${machineId}/config/global`),
  updateGlobal: (machineId, content) => api.put(`/machines/${machineId}/config/global`, { content }),
  getSSH: (machineId) => api.get(`/machines/${machineId}/config/ssh`),
  updateSSH: (machineId, content) => api.put(`/machines/${machineId}/config/ssh`, { content }),
  getSSHHosts: (machineId) => api.get(`/machines/${machineId}/config/ssh-hosts`),
  updateSSHHosts: (machineId, hosts) => api.put(`/machines/${machineId}/config/ssh-hosts`, { hosts })
}
