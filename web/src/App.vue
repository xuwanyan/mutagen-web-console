<template>
  <!-- 登录页 -->
  <div v-if="!loggedIn" class="login-page">
    <div class="login-box">
      <h2>Mutagen Web</h2>
      <p class="login-subtitle">登录</p>
      <input v-model="loginUser" placeholder="用户名" @keyup.enter="doLogin" />
      <input v-model="loginPass" type="password" placeholder="密码" @keyup.enter="doLogin" />
      <button @click="doLogin" :disabled="logging">登录</button>
      <p v-if="loginError" class="login-error">{{ loginError }}</p>
    </div>
  </div>

  <!-- 主界面 -->
  <div v-else class="app">
    <aside class="sidebar">
      <div class="logo">Mutagen Web</div>
      <nav>
        <div class="nav-item" :class="{ active: currentTab === 'machines' }" @click="currentTab = 'machines'">
          机器管理
        </div>
        <div class="nav-item" :class="{ active: currentTab === 'tasks' }" @click="currentTab = 'tasks'">
          同步任务
        </div>
        <div class="nav-item" :class="{ active: currentTab === 'config' }" @click="currentTab = 'config'">
          全局配置
        </div>
      </nav>
    </aside>

    <main class="main">
      <!-- 机器管理 -->
      <section v-if="currentTab === 'machines'" class="section">
        <h2>机器管理</h2>
        <div class="form-inline">
          <input v-model="newMachineName" placeholder="机器名称" />
          <button @click="addMachine">添加机器</button>
        </div>
        <table class="table">
          <thead>
            <tr>
              <th>ID</th>
              <th>机器名称</th>
              <th>在线状态</th>
              <th>系统</th>
              <th>Agent版本</th>
              <th>Token</th>
              <th>操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="m in machines" :key="m.id">
              <td>{{ m.id }}</td>
              <td>{{ m.name }}</td>
              <td><span :class="['badge', m.online ? 'success' : 'gray']">{{ m.online ? '在线' : '离线' }}</span></td>
              <td>{{ m.os || '-' }}</td>
              <td>{{ m.agentVersion || '-' }}</td>
              <td class="token">
                <span>{{ revealed[m.id] ? m.token : maskToken(m.token) }}</span>
                <button class="btn-mini" @click="toggleReveal(m.id)">{{ revealed[m.id] ? '隐藏' : '显示' }}</button>
              </td>
              <td>
                <button class="btn-small" @click="testConnection(m.id)">测试</button>
                <button class="btn-small" @click="downloadAgentPack(m)">下载安装包</button>
                <button class="btn-small" @click="regenerateToken(m.id)">重置Token</button>
                <button class="btn-small danger" @click="openDeleteModal(m)">删除</button>
              </td>
            </tr>
          </tbody>
        </table>
      </section>

      <!-- 同步任务 -->
      <section v-if="currentTab === 'tasks'" class="section">
        <h2>同步任务</h2>
        <div class="form-group">
          <label>选择机器</label>
          <select v-model="selectedMachineId">
            <option value="">请选择</option>
            <option v-for="m in machines" :key="m.id" :value="m.id">{{ m.name }}</option>
          </select>
        </div>

        <div v-if="selectedMachineId" class="task-toolbar">
          <button @click="openCreateTaskModal" class="btn-create">+ 新建任务</button>
          <button class="btn-small" @click="refreshStatus">刷新状态</button>
        </div>
        <div v-if="createTaskModal.show" class="task-form">
          <h3>新建同步任务</h3>
          <div class="form-grid">
            <input v-model="createTaskModal.name" placeholder="任务名称" list="history-name" />
            <input v-model="createTaskModal.alpha" placeholder="本地路径，如 C:tp_test" list="history-alpha" />
            <select v-model="createTaskModal.mode">
              <option value="two-way-resolved">two-way-resolved</option>
              <option value="one-way-replica">one-way-replica</option>
              <option value="one-way-safe">one-way-safe</option>
            </select>
          </div>
          <div class="form-row">
            <label class="field-label">远端主机</label>
            <select v-model="createTaskModal.betaHost" class="host-select">
              <option value="">（直接填完整路径）</option>
              <option v-for="h in sshHosts" :key="h.alias" :value="h.alias">{{ h.alias }}（{{ h.user }}@{{ h.hostName }}）</option>
            </select>
            <input v-model="createTaskModal.betaPath" class="path-input" :placeholder="createTaskModal.betaHost ? '远端路径，如 /hmgdata/ftp_test' : '完整远端路径，如 test-ali:/hmgdata/ftp_test'" list="history-beta" />
            <datalist id="history-beta">
              <option v-for="v in getTaskHistory().beta" :value="v" />
            </datalist>
          </div>
          <div class="form-row">
            <label class="field-label">同步选项</label>
            <label class="inline-check"><input type="checkbox" v-model="createTaskModal.ignoreVcs" /> 忽略 VCS</label>
            <label class="field-label">symlink</label>
            <select v-model="createTaskModal.symlinkMode" class="host-select">
              <option value="portable">portable</option>
              <option value="ignore">ignore</option>
              <option value="posix-raw">posix-raw</option>
            </select>
          </div>
          <div class="form-row start">
            <label class="field-label">忽略规则</label>
            <textarea v-model="createTaskModal.ignorePathsText" rows="3" class="ignore-textarea" placeholder="每行一个 pattern，如：&#10;*.crdownload&#10;*.part&#10;*.tmp"></textarea>
          </div>
          <button @click="createTask">创建任务</button>
          <button class="btn-small" @click="createTaskModal.show = false" style="margin-left:8px">取消</button>
          <datalist id="history-name">
            <option v-for="v in getTaskHistory().name" :value="v" />
          </datalist>
          <datalist id="history-alpha">
            <option v-for="v in getTaskHistory().alpha" :value="v" />
          </datalist>
        </div>

        <table v-if="selectedMachineId && tasks.length" class="table">
          <thead>
            <tr>
              <th>名称</th>
              <th>本地路径</th>
              <th>远端路径</th>
              <th>模式</th>
              <th>状态</th>
              <th>错误</th>
              <th>操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="t in tasks" :key="t.id">
              <td>{{ t.name }}</td>
              <td>{{ t.alpha }}</td>
              <td>{{ t.beta }}</td>
              <td>{{ t.mode }}</td>
              <td><span :class="['badge', t.status ? 'success' : 'gray']">{{ t.status || '未知' }}</span></td>
              <td class="error-cell">
                <span v-if="t.lastError" :title="t.lastError" class="error-text">⚠ 异常</span>
                <span v-else>-</span>
              </td>
              <td>
                <button class="btn-small" @click="pauseTask(t.id)">暂停</button>
                <button class="btn-small" @click="resumeTask(t.id)">恢复</button>
                <button class="btn-small" @click="openEditTaskModal(t)">编辑</button>
                <button class="btn-small" @click="retryTask(t.id)">重建</button>
                <button class="btn-small danger" @click="terminateTask(t.id)">终止</button>
              </td>
            </tr>
          </tbody>
        </table>
        <div v-else-if="selectedMachineId" class="empty-state">暂无任务</div>
      </section>

      <!-- 全局配置 -->
      <section v-if="currentTab === 'config'" class="section">
        <h2>全局配置</h2>
        <div class="form-group">
          <label>选择机器</label>
          <select v-model="configMachineId">
            <option value="">请选择</option>
            <option v-for="m in machines" :key="m.id" :value="m.id">{{ m.name }}</option>
          </select>
        </div>

        <div v-if="configMachineId">
          <h3>.mutagen.yml 默认配置</h3>
          <div class="switch-list">
            <label class="switch-row">
              <input type="checkbox" v-model="globalCfg.disableEmptyRootCheck" />
              <span>禁用空根目录检查（disableEmptyRootCheck）</span>
            </label>
            <div class="switch-row">
              <span>默认文件权限 defaultFileMode</span>
              <input v-model="globalCfg.defaultFileMode" class="mode-input" placeholder="0666" />
            </div>
            <div class="switch-row">
              <span>默认目录权限 defaultDirectoryMode</span>
              <input v-model="globalCfg.defaultDirectoryMode" class="mode-input" placeholder="0777" />
            </div>
          </div>
          <button @click="saveGlobalConfig">保存全局配置</button>

          <h3 style="margin-top: 24px;">SSH 主机</h3>
          <table class="table host-table">
            <thead>
              <tr>
                <th>别名 (Host)</th>
                <th>主机地址 (HostName)</th>
                <th>用户 (User)</th>
                <th>密钥文件 (IdentityFile)</th>
                <th>端口</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="(h, i) in sshHostList" :key="i">
                <td class="readonly-cell">{{ h.alias || '-' }}</td>
                <td class="readonly-cell">{{ h.hostName || '-' }}</td>
                <td class="readonly-cell">{{ h.user || '-' }}</td>
                <td class="readonly-cell">{{ h.identityFile || '-' }}</td>
                <td class="readonly-cell">{{ h.port || '-' }}</td>
                <td>
                  <button class="btn-small" @click="openEditSSHHost(i)">编辑</button>
                  <button class="btn-small danger" @click="removeSSHHost(i)">删除</button>
                </td>
              </tr>
            </tbody>
          </table>
          <div class="host-actions">
            <button class="btn-small" @click="addSSHHost">+ 添加主机</button>
            <button @click="saveSSHHosts">保存所有修改</button>
          </div>
        </div>
      </section>
    </main>

    <!-- 编辑任务弹窗 -->
    <div v-if="editTaskModal.show" class="modal-mask" @click.self="editTaskModal.show = false">
      <div class="modal modal-wide">
        <h3>编辑任务：{{ editTaskModal.name }}</h3>
        <div class="modal-form">
          <div class="form-grid">
            <input v-model="editTaskModal.name" placeholder="任务名称" />
            <input v-model="editTaskModal.alpha" placeholder="本地路径，如 C:\ftp_test" />
            <select v-model="editTaskModal.mode">
              <option value="two-way-resolved">two-way-resolved</option>
              <option value="one-way-replica">one-way-replica</option>
              <option value="one-way-safe">one-way-safe</option>
            </select>
          </div>
          <div class="form-row">
            <label class="field-label">远端路径</label>
            <select v-model="editTaskModal.betaHost" class="host-select">
              <option value="">（直接填完整路径）</option>
              <option v-for="h in sshHosts" :key="h.alias" :value="h.alias">{{ h.alias }}（{{ h.user }}@{{ h.hostName }}）</option>
            </select>
            <input v-model="editTaskModal.betaPath" class="path-input" :placeholder="editTaskModal.betaHost ? '远端路径，如 /hmgdata/ftp_test' : '完整远端路径，如 test-ali:/hmgdata/ftp_test'" />
          </div>
          <div class="form-row">
            <label class="field-label">同步选项</label>
            <label class="inline-check"><input type="checkbox" v-model="editTaskModal.ignoreVcs" /> 忽略 VCS</label>
            <label class="field-label">symlink</label>
            <select v-model="editTaskModal.symlinkMode" class="host-select">
              <option value="portable">portable</option>
              <option value="ignore">ignore</option>
              <option value="posix-raw">posix-raw</option>
            </select>
          </div>
          <div class="form-row start">
            <label class="field-label">忽略规则</label>
            <textarea v-model="editTaskModal.ignorePathsText" rows="3" class="ignore-textarea" placeholder="每行一个 pattern"></textarea>
          </div>
        </div>
        <div class="modal-actions">
          <button class="btn-small" @click="editTaskModal.show = false">取消</button>
          <button @click="updateTask">确认保存</button>
        </div>
      </div>
    </div>

    <!-- SSH 主机编辑弹窗 -->
    <div v-if="editSSHModal.show" class="modal-mask" @click.self="editSSHModal.show = false">
      <div class="modal">
        <h3>编辑 SSH 主机</h3>
        <div class="modal-form">
          <div class="form-row">
            <label class="field-label">别名</label>
            <input v-model="editSSHModal.alias" class="path-input" placeholder="test-ali" />
          </div>
          <div class="form-row">
            <label class="field-label">主机地址</label>
            <input v-model="editSSHModal.hostName" class="path-input" placeholder="139.196.73.189" />
          </div>
          <div class="form-row">
            <label class="field-label">用户</label>
            <input v-model="editSSHModal.user" class="path-input" placeholder="root" />
          </div>
          <div class="form-row">
            <label class="field-label">密钥文件</label>
            <input v-model="editSSHModal.identityFile" class="path-input" placeholder="C:\Users\.ssh\id_rsa" />
          </div>
          <div class="form-row">
            <label class="field-label">端口</label>
            <input v-model="editSSHModal.port" class="path-input port-small" placeholder="22" />
          </div>
        </div>
        <div class="modal-actions">
          <button class="btn-small" @click="editSSHModal.show = false">取消</button>
          <button @click="saveEditSSHHost">确认保存</button>
        </div>
      </div>
    </div>

    <!-- 删除机器确认弹窗 -->
    <div v-if="deleteModal.show" class="modal-mask" @click.self="closeDeleteModal">
      <div class="modal">
        <h3>删除机器：{{ deleteModal.machineName }}</h3>
        <p class="modal-tip" style="color: #dc2626;">将彻底删除该机器及所有任务记录，agent 端也会停止并清除配置。</p>
        <div class="modal-actions">
          <button class="btn-small" @click="closeDeleteModal">取消</button>
          <button class="btn-small danger" @click="confirmDelete">确认删除</button>
        </div>
      </div>
    </div>

    <div v-if="message" :class="['toast', message.type]">{{ message.text }}</div>
  </div>
</template>

<script setup>
import { ref, watch, onMounted } from 'vue'
import { authApi, machineApi, taskApi, configApi } from './api/client.js'

const currentTab = ref('machines')
const machines = ref([])
const tasks = ref([])
const sshHosts = ref([])
const sshHostList = ref([])
const selectedMachineId = ref('')
const configMachineId = ref('')
const newMachineName = ref('')
const message = ref(null)
const revealed = ref({})

const globalCfg = ref({
  disableEmptyRootCheck: true,
  defaultFileMode: '0666',
  defaultDirectoryMode: '0777'
})

const deleteModal = ref({ show: false, machineId: null, machineName: '' })

// 登录相关
const loggedIn = ref(!!localStorage.getItem('auth_token'))
const loginUser = ref('')
const loginPass = ref('')
const loginError = ref('')
const logging = ref(false)

async function doLogin() {
  if (!loginUser.value || !loginPass.value) return
  logging.value = true
  loginError.value = ''
  try {
    const res = await authApi.login(loginUser.value, loginPass.value)
    localStorage.setItem('auth_token', res.data.token)
    loggedIn.value = true
    loadMachines()
    setInterval(loadMachines, 30000)
  } catch (e) {
    loginError.value = '用户名或密码错误'
  } finally {
    logging.value = false
  }
}

// 新建任务弹窗
const createTaskModal = ref({
  show: false,
  name: '',
  alpha: '',
  betaHost: '',
  betaPath: '',
  mode: 'two-way-resolved',
  ignoreVcs: true,
  symlinkMode: 'ignore',
  ignorePathsText: ''
})

// 编辑任务弹窗
const editTaskModal = ref({
  show: false,
  taskId: null,
  name: '',
  alpha: '',
  beta: '',
  betaHost: '',
  betaPath: '',
  mode: 'two-way-resolved',
  ignoreVcs: true,
  symlinkMode: 'ignore',
  ignorePathsText: ''
})

// SSH 主机编辑弹窗
const editSSHModal = ref({
  show: false,
  index: -1,
  alias: '',
  hostName: '',
  user: '',
  identityFile: '',
  port: ''
})

function showMsg(text, type = 'info') {
  message.value = { text, type }
  setTimeout(() => message.value = null, 3000)
}

function maskToken(token) {
  if (!token) return '-'
  if (token.length <= 8) return '••••••'
  return token.slice(0, 4) + '••••••' + token.slice(-4)
}

function toggleReveal(id) {
  revealed.value = { ...revealed.value, [id]: !revealed.value[id] }
}

async function loadMachines() {
  try {
    const res = await machineApi.list()
    machines.value = res.data
  } catch (e) {
    showMsg('加载机器失败: ' + e.message, 'error')
  }
}

async function addMachine() {
  if (!newMachineName.value) return
  try {
    await machineApi.create(newMachineName.value)
    newMachineName.value = ''
    await loadMachines()
    showMsg('机器添加成功')
  } catch (e) {
    showMsg('添加失败: ' + e.message, 'error')
  }
}

function openDeleteModal(m) {
  deleteModal.value = { show: true, machineId: m.id, machineName: m.name, mode: 'records' }
}

function closeDeleteModal() {
  deleteModal.value.show = false
}

async function confirmDelete() {
  const { machineId } = deleteModal.value
  try {
    await machineApi.delete(machineId)
    closeDeleteModal()
    await loadMachines()
    showMsg('机器已删除')
  } catch (e) {
    showMsg('删除失败: ' + e.message, 'error')
  }
}

async function regenerateToken(id) {
  try {
    await machineApi.regenerateToken(id)
    await loadMachines()
    showMsg('Token 已重置')
  } catch (e) {
    showMsg('重置失败: ' + e.message, 'error')
  }
}

async function testConnection(id) {
  try {
    const res = await machineApi.testConnection(id)
    showMsg(res.data.online ? '连接正常' : '机器离线', res.data.online ? 'success' : 'error')
  } catch (e) {
    showMsg('测试失败: ' + e.message, 'error')
  }
}

async function loadTasks() {
  if (!selectedMachineId.value) return
  try {
    const res = await taskApi.list(selectedMachineId.value)
    tasks.value = res.data
  } catch (e) {
    showMsg('加载任务失败: ' + e.message, 'error')
  }
}

async function loadTaskHosts() {
  if (!selectedMachineId.value) { sshHosts.value = []; return }
  try {
    const res = await configApi.getSSHHosts(selectedMachineId.value)
    sshHosts.value = res.data.hosts || []
  } catch (e) {
    sshHosts.value = []
  }
}

// --- 新建任务（弹窗版） ---
function openCreateTaskModal() {
  createTaskModal.value = {
    show: true,
    name: '',
    alpha: '',
    betaHost: '',
    betaPath: '',
    mode: 'two-way-resolved',
    ignoreVcs: true,
    symlinkMode: 'ignore',
    ignorePathsText: ''
  }
}

function resetCreateTaskModal() {
  createTaskModal.value.show = false
}

async function createTask() {
  if (!selectedMachineId.value) return
  const t = createTaskModal.value
  if (!t.name || !t.alpha) {
    showMsg('请填写任务名称和本地路径', 'error')
    return
  }
  if (!/^[a-zA-Z]/.test(t.name)) {
    showMsg('任务名称必须以字母开头', 'error')
    return
  }
  if (!t.betaHost) {
    showMsg('请先在全局配置中添加远端主机', 'error')
    return
  }
  if (!t.betaPath) {
    showMsg('请填写远端路径', 'error')
    return
  }
  const beta = `${t.betaHost}:${t.betaPath}`
  saveTaskHistory("name", t.name)
  saveTaskHistory("alpha", t.alpha)
  saveTaskHistory("beta", beta)
  if (!beta) {
    showMsg('请填写远端路径', 'error')
    return
  }
  const ignorePaths = t.ignorePathsText.split('\n').map(s => s.trim()).filter(Boolean)
  const payload = {
    name: t.name,
    alpha: t.alpha,
    beta,
    mode: t.mode,
    ignoreVcs: t.ignoreVcs,
    symlinkMode: t.symlinkMode,
    ignorePaths
  }
  try {
    await taskApi.create(selectedMachineId.value, payload)
    resetCreateTaskModal()
    await loadTasks()
    showMsg('任务创建命令已下发')
  } catch (e) {
    if (e.response && e.response.status === 409) {
      showMsg('任务名称已存在，请更换名称', 'error')
    } else {
      showMsg('创建失败: ' + e.message, 'error')
    }
  }
}

// --- 重建任务 ---
async function retryTask(taskId) {
  try {
    await taskApi.retry(selectedMachineId.value, taskId)
    showMsg('重建命令已下发')
    setTimeout(loadTasks, 1200)
  } catch (e) {
    showMsg('重建失败: ' + e.message, 'error')
  }
}

// --- 编辑任务 ---
function openEditTaskModal(t) {
  // 解析 beta 为 host + path
  const colonIdx = t.beta.indexOf(':')
  const betaHost = colonIdx > 0 ? t.beta.slice(0, colonIdx) : ''
  const betaPath = colonIdx > 0 ? t.beta.slice(colonIdx + 1) : t.beta
  editTaskModal.value = {
    show: true,
    taskId: t.id,
    name: t.name,
    alpha: t.alpha,
    beta: t.beta,
    betaHost,
    betaPath,
    mode: t.mode || 'two-way-resolved',
    ignoreVcs: t.ignoreVcs,
    symlinkMode: t.symlinkMode || 'ignore',
    ignorePathsText: (t.ignorePaths || []).join('\n')
  }
}

async function updateTask() {
  if (!selectedMachineId.value) return
  const t = editTaskModal.value
  if (!/^[a-zA-Z]/.test(t.name)) {
    showMsg("任务名称必须以字母开头", "error")
    return
  }
  const ignorePaths = t.ignorePathsText.split('\n').map(s => s.trim()).filter(Boolean)
  if (!t.betaHost) {
    showMsg('请选择远端主机', 'error')
    return
  }
  const beta = `${t.betaHost}:${t.betaPath}`
  const payload = {
    name: t.name,
    alpha: t.alpha,
    beta,
    mode: t.mode,
    ignoreVcs: t.ignoreVcs,
    symlinkMode: t.symlinkMode,
    ignorePaths
  }
  try {
    await taskApi.update(selectedMachineId.value, t.taskId, payload)
    editTaskModal.value.show = false
    await loadTasks()
    showMsg('任务已更新')
  } catch (e) {
    if (e.response && e.response.status === 409) {
      showMsg('任务名称已存在，请更换名称', 'error')
    } else {
      showMsg('更新失败: ' + e.message, 'error')
    }
  }
}

async function pauseTask(taskId) {
  try {
    await taskApi.pause(selectedMachineId.value, taskId)
    showMsg('暂停命令已下发')
  } catch (e) {
    showMsg('操作失败: ' + e.message, 'error')
  }
}

async function resumeTask(taskId) {
  try {
    await taskApi.resume(selectedMachineId.value, taskId)
    showMsg('恢复命令已下发')
  } catch (e) {
    showMsg('操作失败: ' + e.message, 'error')
  }
}

async function refreshStatus() {
  try {
    await taskApi.refreshStatus(selectedMachineId.value)
    showMsg('刷新命令已下发')
    setTimeout(loadTasks, 1200)
  } catch (e) {
    showMsg('刷新失败: ' + e.message, 'error')
  }
}

async function terminateTask(taskId) {
  try {
    await taskApi.terminate(selectedMachineId.value, taskId)
    await loadTasks()
    showMsg('任务已终止')
  } catch (e) {
    showMsg('操作失败: ' + e.message, 'error')
  }
}

function buildGlobalYaml() {
  const g = globalCfg.value
  let y = 'sync:\n  defaults:\n'
  y += '    permissions:\n'
  y += `      defaultFileMode: "${g.defaultFileMode}"\n`
  y += `      defaultDirectoryMode: "${g.defaultDirectoryMode}"\n`
  if (g.disableEmptyRootCheck) {
    y += '    safety:\n'
    y += '      disableEmptyRootCheck: true\n'
  }
  return y
}

function parseGlobalYaml(content) {
  if (!content) return
  globalCfg.value.disableEmptyRootCheck = /disableEmptyRootCheck:\s*true/.test(content)
  const fm = content.match(/defaultFileMode:\s*"?([0-7]+)"?/)
  const dm = content.match(/defaultDirectoryMode:\s*"?([0-7]+)"?/)
  if (fm) globalCfg.value.defaultFileMode = fm[1]
  if (dm) globalCfg.value.defaultDirectoryMode = dm[1]
}

async function loadConfig() {
  if (!configMachineId.value) return
  try {
    const [gRes, hRes] = await Promise.all([
      configApi.getGlobal(configMachineId.value),
      configApi.getSSHHosts(configMachineId.value)
    ])
    parseGlobalYaml(gRes.data.content)
    sshHostList.value = hRes.data.hosts || []
  } catch (e) {
    showMsg('加载配置失败: ' + e.message, 'error')
  }
}

async function saveGlobalConfig() {
  try {
    await configApi.updateGlobal(configMachineId.value, buildGlobalYaml())
    showMsg('全局配置已保存')
  } catch (e) {
    showMsg('保存失败: ' + e.message, 'error')
  }
}

function addSSHHost() {
  sshHostList.value.push({ alias: '', hostName: '', user: '', identityFile: '', port: '' })
  // 新增的空行自动进入编辑模式
  const idx = sshHostList.value.length - 1
  openEditSSHHost(idx)
}

function removeSSHHost(i) {
  sshHostList.value.splice(i, 1)
}

// --- SSH 主机编辑弹窗 ---
function openEditSSHHost(i) {
  const h = sshHostList.value[i]
  editSSHModal.value = {
    show: true,
    index: i,
    alias: h.alias || '',
    hostName: h.hostName || '',
    user: h.user || '',
    identityFile: h.identityFile || '',
    port: h.port || ''
  }
}

function saveEditSSHHost() {
  const m = editSSHModal.value
  if (!m.alias) {
    showMsg('请填写主机别名', 'error')
    return
  }
  if (m.index >= 0 && m.index < sshHostList.value.length) {
    sshHostList.value[m.index] = {
      alias: m.alias,
      hostName: m.hostName,
      user: m.user,
      identityFile: m.identityFile,
      port: m.port
    }
  }
  editSSHModal.value.show = false
  showMsg('SSH 主机已修改，点击「保存所有修改」生效')
}

async function saveSSHHosts() {
  try {
    await configApi.updateSSHHosts(configMachineId.value, sshHostList.value)
    showMsg('SSH 主机已保存')
    // 同步刷新任务 tab 的主机列表
    if (selectedMachineId.value === configMachineId.value) {
      const res = await configApi.getSSHHosts(configMachineId.value)
      sshHosts.value = res.data.hosts || []
    }
  } catch (e) {
    showMsg('保存失败: ' + e.message, 'error')
  }
}

const HISTORY_KEY_TASK = "mutagen_task_history";
function getTaskHistory() {
  try { return JSON.parse(localStorage.getItem(HISTORY_KEY_TASK) || "{}"); }
  catch { return {}; }
}
function saveTaskHistory(field, value) {
  if (!value) return;
  const h = getTaskHistory();
  if (!h[field]) h[field] = [];
  h[field] = [value, ...h[field].filter(v => v !== value)].slice(0, 10);
  localStorage.setItem(HISTORY_KEY_TASK, JSON.stringify(h));
}
function downloadAgentPack(m) {
  window.open(`/api/machines/${m.id}/agent-pack?token=${localStorage.getItem("auth_token")}`, "_blank")
}

watch(selectedMachineId, () => { loadTasks(); loadTaskHosts() })
watch(configMachineId, loadConfig)

onMounted(() => {
  if (loggedIn.value) {
    loadMachines()
    setInterval(loadMachines, 30000)
  }
})
</script>

<style scoped>
.app { display: flex; min-height: 100vh; }

/* 登录页 */
.login-page { display: flex; align-items: center; justify-content: center; min-height: 100vh; background: #1a1a2e; }
.login-box { background: #fff; padding: 40px; border-radius: 8px; box-shadow: 0 10px 40px rgba(0,0,0,0.2); width: 360px; text-align: center; }
.login-box h2 { margin: 0 0 8px; color: #1a1a2e; }
.login-subtitle { color: #888; margin-bottom: 24px; font-size: 14px; }
.login-box input { display: block; width: 100%; padding: 12px; margin-bottom: 12px; border: 1px solid #ddd; border-radius: 4px; box-sizing: border-box; }
.login-box button { width: 100%; padding: 12px; background: #2563eb; color: #fff; border: none; border-radius: 4px; cursor: pointer; font-size: 16px; }
.login-box button:hover { background: #1d4ed8; }
.login-box button:disabled { background: #94a3b8; }
.login-error { color: #dc2626; margin-top: 12px; font-size: 14px; }

.app { display: flex; min-height: 100vh; }
.sidebar { width: 220px; background: #1a1a2e; color: #fff; padding: 20px; }
.logo { font-size: 20px; font-weight: bold; margin-bottom: 30px; }
.nav-item { padding: 12px 16px; margin: 4px 0; border-radius: 6px; cursor: pointer; transition: 0.2s; }
.nav-item:hover { background: #2d2d44; }
.nav-item.active { background: #4a4a6a; }

.main { flex: 1; padding: 20px 30px; }
.section { background: #fff; padding: 20px 0; }
h2 { margin-bottom: 20px; color: #333; }
h3 { margin: 20px 0 12px; color: #555; font-size: 16px; }

.form-inline { display: flex; gap: 10px; margin-bottom: 20px; }
.form-inline input { flex: 1; max-width: 400px; }
.form-group { margin-bottom: 20px; }
.form-group label { display: block; margin-bottom: 6px; color: #666; }
.form-group select, .form-group input { width: 400px; padding: 8px 12px; border: 1px solid #ddd; border-radius: 4px; }

.form-grid { display: grid; grid-template-columns: repeat(3, 1fr); gap: 20px; margin-bottom: 20px; align-items: center; }
.form-grid input, .form-grid select { padding: 8px 12px; border: 1px solid #ddd; border-radius: 4px; }
.form-row { display: flex; gap: 12px; align-items: center; margin-bottom: 12px; }
.form-row.start { align-items: flex-start; }
.field-label { color: #666; white-space: nowrap; min-width: 70px; }
.inline-check { display: flex; align-items: center; gap: 6px; color: #666; }
.host-select { padding: 10px 14px; border: 1px solid #ddd; border-radius: 4px; min-width: 220px; font-size: 15px; }
.path-input { flex: 1; padding: 10px 14px; border: 1px solid #ddd; border-radius: 4px; font-size: 15px; }
.port-small { max-width: 100px; }
.ignore-textarea { flex: 1; padding: 12px; border: 1px solid #ddd; border-radius: 4px; font-family: monospace; font-size: 15px; resize: vertical; }

textarea { width: 100%; padding: 12px; border: 1px solid #ddd; border-radius: 4px; font-family: monospace; font-size: 14px; resize: vertical; }

.switch-list { border: 1px solid #eee; border-radius: 6px; padding: 8px 16px; margin-bottom: 12px; max-width: 560px; }
.switch-row { display: flex; align-items: center; gap: 10px; padding: 10px 0; border-bottom: 1px solid #f3f4f6; color: #444; }
.switch-row:last-child { border-bottom: none; }
.mode-input { margin-left: auto; width: 120px; padding: 6px 10px; border: 1px solid #ddd; border-radius: 4px; font-family: monospace; }

.host-table .readonly-cell { padding: 12px; color: #333; font-size: 13px; }
.host-table input { width: 100%; padding: 6px 8px; border: 1px solid #ddd; border-radius: 4px; box-sizing: border-box; }
.host-table .port-input { width: 70px; }
.host-actions { display: flex; gap: 12px; margin-top: 12px; align-items: center; }

.task-toolbar { display: flex; gap: 12px; margin: 16px 0; align-items: center; }
.task-form { background: #f8fafc; border: 1px solid #e2e8f0; border-radius: 8px; padding: 24px; margin: 16px 0; }
.btn-create { padding: 10px 20px; background: #059669; }
.btn-create:hover { background: #047857; }

button { padding: 8px 16px; background: #2563eb; color: #fff; border: none; border-radius: 4px; cursor: pointer; }
button:hover { background: #1d4ed8; }
.btn-small { padding: 8px 18px; font-size: 15px; margin-right: 6px; background: #64748b; }
.btn-small:hover { background: #475569; }
.btn-small.danger { background: #dc2626; }
.btn-small.danger:hover { background: #b91c1c; }
.btn-mini { padding: 6px 12px; font-size: 14px; margin-left: 8px; background: #94a3b8; }
.btn-mini:hover { background: #64748b; }

.table { width: 100%; border-collapse: collapse; margin-top: 16px; }
.table th, .table td { padding: 12px; text-align: left; border-bottom: 1px solid #eee; }
.table th { background: #f8fafc; color: #333; font-weight: 600; font-size: 14px; }
.token { font-family: monospace; font-size: 14px; color: #666; }

.badge { display: inline-block; padding: 6px 14px; border-radius: 12px; font-size: 14px; }
.badge.success { background: #dcfce7; color: #166534; }
.badge.gray { background: #f3f4f6; color: #6b7280; }

.error-cell { font-size: 14px; }
.error-text { color: #dc2626; cursor: help; border-bottom: 1px dotted #dc2626; }

.empty-state { text-align: center; color: #999; padding: 40px; }

.modal-wide { width: 90vw; max-width: 1200px; max-height: 85vh; overflow-y: auto; }
.modal-form { padding: 10px 0; }
.modal-mask { position: fixed; inset: 0; background: rgba(0,0,0,0.45); display: flex; align-items: center; justify-content: center; z-index: 2000; }
.modal { background: #fff; border-radius: 8px; padding: 28px; width: 520px; max-width: 95vw; box-shadow: 0 10px 40px rgba(0,0,0,0.2); }
.modal h3 { margin: 0 0 12px; }
.modal-tip { color: #666; margin-bottom: 12px; }
.radio-row { display: flex; align-items: flex-start; gap: 10px; padding: 10px; border: 1px solid #eee; border-radius: 6px; margin-bottom: 10px; cursor: pointer; color: #444; }
.radio-row input { margin-top: 3px; }
.modal-actions { display: flex; justify-content: flex-end; gap: 10px; margin-top: 16px; }

.toast { position: fixed; top: 20px; right: 20px; padding: 12px 20px; border-radius: 6px; color: #fff; z-index: 3000; }
.toast.info { background: #2563eb; }
.toast.success { background: #16a34a; }
.toast.error { background: #dc2626; }
</style>