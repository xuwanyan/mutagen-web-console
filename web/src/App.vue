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
      <!-- 顶栏：server 级操作（不依赖选中机器） -->
      <div class="topbar">
        <button class="btn-small" @click="backupData" title="下载 server 端 data.json 备份">⬇ 下载服务端数据</button>
      </div>
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
        <h2>同步任务 <span v-if="selectedMachineId" class="task-count">{{ tasks.length }}</span></h2>
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
          <button class="btn-small" @click="copyCreateCommand">复制全部任务创建命令</button>
          <button class="btn-small" :disabled="refreshAgents.loading" @click="refreshRemoteAgents">{{ refreshAgents.loading ? '推送中…' : '推送新 Agent 给远端' }}</button>
          <button class="btn-small" :disabled="allPaused" :class="{ 'btn-disabled': allPaused }" @click="openPauseAllModal">暂停全部</button>
          <button class="btn-small" :disabled="nonePaused" :class="{ 'btn-disabled': nonePaused }" @click="openResumeAllModal">恢复全部</button>
          <button class="btn-small danger" @click="openTerminateAllModal">终止全部</button>
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

        <div v-if="selectedMachineId && tasks.length" class="table-card">
          <table class="table">
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
            <tr v-for="t in pagedTasks" :key="t.id" :class="{ 'row-warning': hasDuplicateName(t) }">
              <td>
                <span :title="hasDuplicateName(t) ? '存在同名任务（mutagen 允许重名），可能引起管理歧义，建议重建后删除冗余任务' : ''">{{ t.name }}</span>
                <span v-if="hasDuplicateName(t)" class="dup-mark" title="存在同名任务">⚠</span>
              </td>
              <td>{{ t.alpha }}</td>
              <td>{{ t.beta }}</td>
              <td>{{ t.mode }}</td>
              <td><span :class="['badge', t.status ? 'success' : 'gray']">{{ t.status || '未知' }}</span></td>
              <td class="error-cell">
                <span v-if="t.lastError" :title="t.lastError" class="error-text">⚠ 异常</span>
                <span v-if="t.transitionProblems && t.transitionProblems.length" class="transition-problems-badge" :title="t.transitionProblems.map(p => p.path + ': ' + p.error).join('\n')">
                  ⚠ {{ t.transitionProblems.length }} 个同步问题
                </span>
                <span v-if="!t.lastError && (!t.transitionProblems || !t.transitionProblems.length)">-</span>
              </td>
              <td>
                <button class="btn-small" :disabled="isPaused(t)" :class="{ 'btn-disabled': isPaused(t) }" @click="pauseTask(t.id)">暂停</button>
                <button class="btn-small" :disabled="!isPaused(t)" :class="{ 'btn-disabled': !isPaused(t) }" @click="resumeTask(t.id)">恢复</button>
                <button class="btn-small" @click="openEditTaskModal(t)">编辑</button>
                <button class="btn-small" @click="retryTask(t.id)">重建</button>
                <button class="btn-small danger" @click="openTerminateModal(t.id)">终止</button>
              </td>
            </tr>
          </tbody>
        </table>
        </div>
        <div v-if="selectedMachineId && tasks.length > pageSize" class="pagination">
          <button class="btn-small" :disabled="currentPage <= 1" @click="currentPage--">上一页</button>
          <span>第 {{ currentPage }} / {{ totalPages }} 页（共 {{ tasks.length }} 个任务）</span>
          <button class="btn-small" :disabled="currentPage >= totalPages" @click="currentPage++">下一页</button>
        </div>
        <div v-if="selectedMachineId && !tasks.length" class="empty-state">暂无任务</div>
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
            <button class="btn-small" @click="importSSHHosts">从本机导入</button>
            <button @click="saveSSHHosts">保存所有修改</button>
          </div>

          <h3 style="margin-top: 24px;">本地落盘前备份配置</h3>
          <div class="switch-list">
            <label class="switch-row">
              <input type="checkbox" v-model="backupCfg.enabled" />
              <span>启用落盘前备份（enabled）</span>
            </label>
            <div v-if="backupCfg.enabled" class="sub-options">
              <div class="switch-row">
                <span>备份目录 dir（留空则用 &lt;同步根&gt;.mutagen-backup）</span>
                <input v-model="backupCfg.dir" class="mode-input" style="width: 320px;" placeholder="例: D:\mutagen-backup" />
              </div>
              <div class="switch-row">
                <span>保留天数 retentionDays（0 表示不清理）</span>
                <input v-model.number="backupCfg.retentionDays" type="number" min="0" class="mode-input" placeholder="7" />
              </div>
              <label class="switch-row">
                <input type="checkbox" v-model="backupCfg.failOpen" />
                <span>备份失败时仍继续同步（failOpen）</span>
              </label>
            </div>
          </div>
          <button @click="saveBackupConfig">保存本机备份配置</button>
          <button class="btn-small" style="margin-left:8px" :disabled="backupVerify.loading" @click="verifyBackup">{{ backupVerify.loading ? '校验中…' : '一键校验本机 backup.json' }}</button>
          <p class="hint">保存后仅写入本机 ~/.mutagen/backup.json，不会自动重启会话；需 pause/resume 对应任务后生效。</p>
          <pre v-if="backupVerify.report" class="verify-report" :class="{ ok: backupVerify.ok, bad: !backupVerify.ok }">{{ backupVerify.report }}</pre>

          <h3 style="margin-top: 24px;">远端 Linux 备份配置</h3>
          <div v-if="backupLinuxLocked" class="switch-row" style="color: #b45309; font-size: 12px; margin-bottom: 8px;">
            ⚠ 远端已存在 backup.json，配置已锁定（使用现有配置）。如需修改请先在远端删除 ~/.mutagen/backup.json 再保存。
          </div>
          <div class="switch-list">
            <label class="switch-row">
              <input type="checkbox" v-model="backupLinuxCfg.enabled" :disabled="backupLinuxLocked" />
              <span>启用远端备份（enabled）</span>
            </label>
            <div v-if="backupLinuxCfg.enabled" class="sub-options">
              <div class="switch-row">
                <span>备份目录 dir（留空则用 &lt;同步根&gt;.mutagen-backup）</span>
                <input v-model="backupLinuxCfg.dir" class="mode-input" style="width: 320px;" placeholder="例: /data/mutagen-backup" :disabled="backupLinuxLocked" />
              </div>
              <div class="switch-row">
                <span>保留天数 retentionDays（0 表示不清理）</span>
                <input v-model.number="backupLinuxCfg.retentionDays" type="number" min="0" class="mode-input" placeholder="7" :disabled="backupLinuxLocked" />
              </div>
              <label class="switch-row">
                <input type="checkbox" v-model="backupLinuxCfg.failOpen" :disabled="backupLinuxLocked" />
                <span>备份失败时仍继续同步（failOpen）</span>
              </label>
            </div>
          </div>
          <button @click="saveBackupLinuxConfig">保存 远端Linux 备份配置（仅推送 SSH 主机）</button>
          <button class="btn-small" style="margin-left:8px" :disabled="backupLinuxVerify.loading" @click="verifyBackupLinux">{{ backupLinuxVerify.loading ? '校验中…' : '一键校验远端 backup.json' }}</button>
          <p class="hint">保存后仅推送 SSH 主机的 ~/.mutagen/backup.json，不会修改 Windows 本机配置。需 pause/resume 对应任务后生效。</p>
          <pre v-if="backupLinuxVerify.report" class="verify-report" :class="{ ok: backupLinuxVerify.ok, bad: !backupLinuxVerify.ok }">{{ backupLinuxVerify.report }}</pre>
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

    <!-- 终止全部任务确认弹窗 -->
    <div v-if="terminateAllModal.show" class="modal-mask" @click.self="closeTerminateAllModal">
      <div class="modal">
        <h3>终止全部任务</h3>
        <p class="modal-tip" style="color: #dc2626;">将终止当前机器下所有同步任务并删除记录，此操作不可恢复！</p>
        <div class="modal-actions">
          <button class="btn-small" @click="closeTerminateAllModal">取消</button>
          <button class="btn-small danger" @click="confirmTerminateAll">确认终止全部</button>
        </div>
      </div>
    </div>

    <!-- 终止单个任务确认弹窗 -->
    <div v-if="terminateModal.show" class="modal-mask" @click.self="closeTerminateModal">
      <div class="modal">
        <h3>终止任务</h3>
        <p class="modal-tip" style="color: #dc2626;">将终止该同步任务并删除记录，此操作不可恢复！</p>
        <div class="modal-actions">
          <button class="btn-small" @click="closeTerminateModal">取消</button>
          <button class="btn-small danger" @click="confirmTerminate">确认终止</button>
        </div>
      </div>
    </div>

    <!-- 暂停全部任务确认弹窗 -->
    <div v-if="pauseAllModal.show" class="modal-mask" @click.self="closePauseAllModal">
      <div class="modal">
        <h3>暂停全部任务</h3>
        <p class="modal-tip">将暂停当前机器下所有同步任务，已暂停的任务会保持暂停状态。</p>
        <div class="modal-actions">
          <button class="btn-small" @click="closePauseAllModal">取消</button>
          <button class="btn-small" @click="confirmPauseAll">确认暂停全部</button>
        </div>
      </div>
    </div>

    <!-- 恢复全部任务确认弹窗 -->
    <div v-if="resumeAllModal.show" class="modal-mask" @click.self="closeResumeAllModal">
      <div class="modal">
        <h3>恢复全部任务</h3>
        <p class="modal-tip">将恢复当前机器下所有已暂停的同步任务。</p>
        <div class="modal-actions">
          <button class="btn-small" @click="closeResumeAllModal">取消</button>
          <button class="btn-small" @click="confirmResumeAll">确认恢复全部</button>
        </div>
      </div>
    </div>

    <!-- 远端备份配置提示弹窗 -->
    <div v-if="backupLinuxAlertModal.show" class="modal-mask" @click.self="backupLinuxAlertModal.show = false">
      <div class="modal">
        <h3>{{ backupLinuxAlertModal.title }}</h3>
        <p class="modal-tip" style="color: #b45309; white-space: pre-wrap;">{{ backupLinuxAlertModal.message }}</p>
        <div v-if="backupLinuxAlertModal.report" style="margin-top: 12px; background: #f3f4f6; padding: 8px; border-radius: 4px; font-size: 12px; color: #4b5563; white-space: pre-wrap; max-height: 200px; overflow-y: auto;">{{ backupLinuxAlertModal.report }}</div>
        <div class="modal-actions">
          <button class="btn-small" @click="backupLinuxAlertModal.show = false">我知道了</button>
        </div>
      </div>
    </div>

    <div v-if="message" :class="['toast', message.type]">{{ message.text }}</div>
  </div>
</template>

<script setup>
import { ref, computed, watch, onMounted, onBeforeUnmount } from 'vue'
import { authApi, machineApi, taskApi, configApi } from './api/client.js'

const currentTab = ref('machines')
const machines = ref([])
const tasks = ref([])
const pageSize = 50
const currentPage = ref(1)
const totalPages = computed(() => Math.max(1, Math.ceil(tasks.value.length / pageSize)))
const pagedTasks = computed(() => {
  const start = (currentPage.value - 1) * pageSize
  return tasks.value.slice(start, start + pageSize)
})
// 全部已暂停 → 暂停全部按钮变灰；没有任何暂停任务 → 恢复全部按钮变灰
const allPaused = computed(() => tasks.value.length > 0 && tasks.value.every(t => isPaused(t)))
const nonePaused = computed(() => tasks.value.length > 0 && !tasks.value.some(t => isPaused(t)))
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

const backupCfg = ref({
  enabled: false,
  dir: '',
  retentionDays: 7,
  failOpen: true
})

const backupVerify = ref({ loading: false, report: '', ok: false })

const backupLinuxCfg = ref({
  enabled: false,
  dir: '',
  retentionDays: 7,
  failOpen: true
})
// 远端已存在 backup.json 时锁定配置（使用 A 留下的配置，B 不能改）
const backupLinuxLocked = ref(false)

const backupLinuxVerify = ref({ loading: false, report: '', ok: false })

const deleteModal = ref({ show: false, machineId: null, machineName: '' })
const terminateAllModal = ref({ show: false })
const terminateModal = ref({ show: false, taskId: null })
const pauseAllModal = ref({ show: false })
const resumeAllModal = ref({ show: false })
// 远端备份配置提示弹窗（远端已存在 backup.json 等场景）
const backupLinuxAlertModal = ref({ show: false, title: '', message: '', report: '' })

// 登录相关
const loggedIn = ref(!!localStorage.getItem('auth_token'))

// 轮询 interval ID，防止多次登录后 setInterval 叠加导致轮询倍速
let machinesTimerId = null
let tasksTimerId = null

// 一次性定时器追踪：组件卸载时统一清理，避免卸载后回调操作已销毁的响应式状态
const pendingTimers = new Set()
function later(fn, ms) {
  const id = setTimeout(() => { pendingTimers.delete(id); fn() }, ms)
  pendingTimers.add(id)
  return id
}

function startMachinesPolling() {
  if (machinesTimerId) clearInterval(machinesTimerId)
  machinesTimerId = setInterval(loadMachines, 30000)
}

function startTasksPolling() {
  if (tasksTimerId) clearInterval(tasksTimerId)
  tasksTimerId = setInterval(() => {
    if (loggedIn.value && currentTab.value === 'tasks' && selectedMachineId.value) {
      loadTasks()
    }
  }, 10000)
}

function stopAllPolling() {
  if (machinesTimerId) { clearInterval(machinesTimerId); machinesTimerId = null }
  if (tasksTimerId) { clearInterval(tasksTimerId); tasksTimerId = null }
}
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
    startMachinesPolling()
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
  later(() => message.value = null, 3000)
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

// 乐观更新：操作后 15 秒内不接收轮询对 status 的覆盖，避免状态闪烁
const optimisticTasks = ref({}) // { taskId: timestamp }

function setOptimistic(taskId) {
  optimisticTasks.value[taskId] = Date.now()
  later(() => { delete optimisticTasks.value[taskId] }, 15000)
}

async function loadTasks() {
  if (!selectedMachineId.value) return
  try {
    const res = await taskApi.list(selectedMachineId.value)
    const serverTasks = res.data || []
    const now = Date.now()
    // 乐观窗口内的任务：保留本地 status，不覆盖
    serverTasks.forEach(t => {
      const optTime = optimisticTasks.value[t.id]
      if (optTime && now - optTime < 15000) {
        const local = tasks.value.find(x => x.id === t.id)
        if (local) t.status = local.status
      }
    })
    tasks.value = serverTasks
    // 页码边界保护：删除任务后当前页可能越界
    if (currentPage.value > totalPages.value) currentPage.value = totalPages.value
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
    later(loadTasks, 1200)
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
  if (!t.betaPath) {
    showMsg('请填写远端路径', 'error')
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
    const resp = await taskApi.update(selectedMachineId.value, t.taskId, payload)
    editTaskModal.value.show = false
    // 立即用返回数据更新本地列表，后台异步重建
    const idx = tasks.value.findIndex(x => x.id === t.taskId)
    if (idx >= 0 && resp.data.task) {
      tasks.value[idx] = { ...tasks.value[idx], ...resp.data.task }
    }
    showMsg('任务已更新，后台重建中')
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
    const t = tasks.value.find(x => x.id === taskId)
    if (t) t.status = '[Paused]'
    setOptimistic(taskId)
    showMsg('暂停命令已下发')
  } catch (e) {
    showMsg('操作失败: ' + e.message, 'error')
  }
}

// isPaused 判断任务是否处于暂停状态。
// mutagen sync list -l 输出 "Status: [Paused]"（带方括号）。
function isPaused(t) {
  if (!t || !t.status) return false
  // 兼容 "[Paused]" / "Paused" / "paused" 等形式
  const s = t.status.toLowerCase().replace(/[\[\]]/g, '')
  return s === 'paused'
}

// hasDuplicateName 检测当前任务在 tasks 列表中是否存在同名兄弟。
// 用于标记 mutagen 允许但 Web 层不推荐的同名会话。
function hasDuplicateName(t) {
  if (!t || !t.name) return false
  return tasks.value.filter(x => x.name === t.name).length > 1
}

// 单任务终止二次确认
function openTerminateModal(taskId) {
  terminateModal.value = { show: true, taskId }
}

function closeTerminateModal() {
  terminateModal.value.show = false
}

async function confirmTerminate() {
  const taskId = terminateModal.value.taskId
  closeTerminateModal()
  await terminateTask(taskId)
}

function openPauseAllModal() {
  pauseAllModal.value = { show: true }
}

function closePauseAllModal() {
  pauseAllModal.value.show = false
}

async function confirmPauseAll() {
  closePauseAllModal()
  await pauseAllTasks()
}

function openResumeAllModal() {
  resumeAllModal.value = { show: true }
}

function closeResumeAllModal() {
  resumeAllModal.value.show = false
}

async function confirmResumeAll() {
  closeResumeAllModal()
  await resumeAllTasks()
}

async function pauseAllTasks() {
  try {
    const res = await taskApi.pauseAll(selectedMachineId.value)
    tasks.value.forEach(t => { t.status = '[Paused]'; setOptimistic(t.id) })
    showMsg(`已下发 ${res.data.sent} 个暂停命令${res.data.failed ? '，失败 ' + res.data.failed : ''}`)
  } catch (e) {
    showMsg('操作失败: ' + e.message, 'error')
  }
}

async function resumeAllTasks() {
  try {
    const res = await taskApi.resumeAll(selectedMachineId.value)
    tasks.value.forEach(t => { t.status = '恢复中'; setOptimistic(t.id) })
    showMsg(`已下发 ${res.data.sent} 个恢复命令${res.data.failed ? '，失败 ' + res.data.failed : ''}`)
  } catch (e) {
    showMsg('操作失败: ' + e.message, 'error')
  }
}

function openTerminateAllModal() {
  terminateAllModal.value = { show: true }
}

function closeTerminateAllModal() {
  terminateAllModal.value.show = false
}

async function confirmTerminateAll() {
  closeTerminateAllModal()
  try {
    const res = await taskApi.terminateAll(selectedMachineId.value)
    showMsg(`已终止 ${res.data.terminated} 个任务${res.data.failed ? '，失败 ' + res.data.failed : ''}`)
    await loadTasks()
  } catch (e) {
    showMsg('操作失败: ' + e.message, 'error')
  }
}

async function backupData() {
  try {
    const res = await taskApi.backupData()
    const blob = new Blob([res.data], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    const filename = res.headers['content-disposition']
      ? res.headers['content-disposition'].match(/filename="?(.+?)"?$/)?.[1] || 'mutagen-web-data.json'
      : 'mutagen-web-data.json'
    a.download = filename
    a.click()
    URL.revokeObjectURL(url)
    showMsg('数据备份已下载')
  } catch (e) {
    showMsg('备份失败: ' + e.message, 'error')
  }
}

async function copyCreateCommand() {
  try {
    const res = await taskApi.getCommandTemplate(selectedMachineId.value)
    const cmds = res.data.commands || []
    const hosts = res.data.hosts || []
    let text = ''
    if (cmds.length === 0) {
      text += '# 当前机器下暂无同步任务\n'
    } else {
      cmds.forEach(c => { text += c + '\n' })
    }
    if (hosts.length > 0) {
      text += '\n# SSH 主机别名（恢复前请先配置 ssh config）：\n'
      hosts.forEach(h => {
        text += `#   Host ${h.alias}\n`
        if (h.hostName) text += `#     HostName ${h.hostName}\n`
        if (h.user) text += `#     User ${h.user}\n`
        if (h.port) text += `#     Port ${h.port}\n`
        if (h.identityFile) text += `#     IdentityFile ${h.identityFile}\n`
      })
    }
    await navigator.clipboard.writeText(text)
    showMsg(`已复制 ${cmds.length} 条创建命令到剪贴板`)
  } catch (e) {
    showMsg('复制失败: ' + e.message, 'error')
  }
}

async function resumeTask(taskId) {
  try {
    await taskApi.resume(selectedMachineId.value, taskId)
    const t = tasks.value.find(x => x.id === taskId)
    if (t) t.status = '恢复中'
    setOptimistic(taskId)
    showMsg('恢复命令已下发')
  } catch (e) {
    showMsg('操作失败: ' + e.message, 'error')
  }
}

async function refreshStatus() {
  try {
    await taskApi.refreshStatus(selectedMachineId.value)
    showMsg('刷新命令已下发')
    later(loadTasks, 1200)
  } catch (e) {
    showMsg('刷新失败: ' + e.message, 'error')
  }
}

// 用户主动触发：推送新 agent 给远端
// 调用 /api/machines/:id/refresh-remote-agents，让 agent 立即检查
// mutagen.exe + mutagen-agents.tar.gz 指纹，变化则 SSH 删远端 agents 目录。
const refreshAgents = ref({ loading: false })

async function refreshRemoteAgents() {
  if (!selectedMachineId.value) return
  if (!confirm('将检查 mutagen 版本并清理远端 agent（如本地版本变化会删除远端 ~/.mutagen/agents/ 目录，下次连接时自动重装）。确认执行？')) {
    return
  }
  refreshAgents.value.loading = true
  try {
    const res = await taskApi.refreshRemoteAgents(selectedMachineId.value)
    showMsg('推送完成: ' + (res.data?.message || '远端 agent 已检查'))
  } catch (e) {
    let errMsg = e.message
    if (e.response?.data?.error) errMsg = e.response.data.error
    showMsg('推送失败: ' + errMsg, 'error')
  } finally {
    refreshAgents.value.loading = false
  }
}

async function terminateTask(taskId) {
  try {
    await taskApi.terminate(selectedMachineId.value, taskId)
    // 立即从本地列表删除，不等轮询
    tasks.value = tasks.value.filter(x => x.id !== taskId)
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
    const [gRes, hRes, bRes, blRes] = await Promise.all([
      configApi.getGlobal(configMachineId.value),
      configApi.getSSHHosts(configMachineId.value),
      configApi.getBackup(configMachineId.value),
      configApi.getBackupLinux(configMachineId.value)
    ])
    parseGlobalYaml(gRes.data.content)
    sshHostList.value = hRes.data.hosts || []
    backupCfg.value = {
      enabled: !!bRes.data.enabled,
      dir: bRes.data.dir || '',
      retentionDays: bRes.data.retentionDays ?? 7,
      failOpen: bRes.data.failOpen !== false
    }
    backupVerify.value = { loading: false, report: '', ok: false }
    backupLinuxCfg.value = {
      enabled: !!blRes.data.enabled,
      dir: blRes.data.dir || '',
      retentionDays: blRes.data.retentionDays ?? 7,
      failOpen: blRes.data.failOpen !== false
    }
    backupLinuxVerify.value = { loading: false, report: '', ok: false }
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

async function saveBackupConfig() {
  try {
    const res = await configApi.updateBackup(configMachineId.value, {
      enabled: backupCfg.value.enabled,
      dir: backupCfg.value.dir,
      retentionDays: Number(backupCfg.value.retentionDays) || 0,
      failOpen: backupCfg.value.failOpen
    })
    showMsg('备份配置已下发' + (res.data.commandId ? '（已推送 agent）' : ''))
  } catch (e) {
    const saved = e.response && e.response.data && e.response.data.saved
    showMsg('保存失败: ' + (e.response?.data?.error || e.message) + (saved ? '（已落库，agent 可能离线）' : ''), 'error')
  }
}

async function verifyBackup() {
  backupVerify.value = { loading: true, report: '', ok: false }
  try {
    const res = await configApi.verifyBackup(configMachineId.value)
    backupVerify.value = {
      loading: false,
      report: res.data.report || res.data.error || '(无返回)',
      ok: !!res.data.success
    }
    showMsg(res.data.success ? '校验通过：各端一致' : '校验发现不一致或错误', res.data.success ? 'success' : 'error')
  } catch (e) {
    backupVerify.value = { loading: false, report: e.response?.data?.error || e.message, ok: false }
    showMsg('校验失败: ' + (e.response?.data?.error || e.message), 'error')
  }
}

async function saveBackupLinuxConfig() {
  try {
    const res = await configApi.updateBackupLinux(configMachineId.value, {
      enabled: backupLinuxCfg.value.enabled,
      dir: backupLinuxCfg.value.dir,
      retentionDays: Number(backupLinuxCfg.value.retentionDays) || 0,
      failOpen: backupLinuxCfg.value.failOpen
    })
    const existingDir = res.data.existingDir || ''
    const report = res.data.report || ''
    if (existingDir) {
      // 远端已存在 backup.json，回填远端现有配置 + 锁定
      // server 已把远端配置落库，这里重新拉取一次保证 UI 与 DB 一致
      const blRes = await configApi.getBackupLinux(configMachineId.value)
      backupLinuxCfg.value = {
        enabled: !!blRes.data.enabled,
        dir: blRes.data.dir || '',
        retentionDays: blRes.data.retentionDays ?? 7,
        failOpen: blRes.data.failOpen !== false
      }
      backupLinuxLocked.value = true
      backupLinuxAlertModal.value = {
        show: true,
        title: '远端已存在备份配置',
        message: `远端 ~/.mutagen/backup.json 已存在，未覆盖，已将现有配置回传并锁定本页。\n\n当前使用的远端配置：\n  dir: ${existingDir}\n\n如需修改，请先在远端删除该文件后重新保存：\n  rm ~/.mutagen/backup.json\n\n（多台 Win 同步到同一 Linux 时共用同一份备份配置；备份子目录按同步路径自动区分，数据不会冲突。）`,
        report: report
      }
    } else {
      // 远端不存在或正常写入，解锁
      backupLinuxLocked.value = false
      showMsg('Linux 备份配置已下发' + (res.data.commandId ? '（已推送 SSH 主机）' : ''))
    }
  } catch (e) {
    const saved = e.response && e.response.data && e.response.data.saved
    showMsg('保存失败: ' + (e.response?.data?.error || e.message) + (saved ? '（已落库，agent 可能离线）' : ''), 'error')
  }
}

async function verifyBackupLinux() {
  backupLinuxVerify.value = { loading: true, report: '', ok: false }
  try {
    const res = await configApi.verifyBackupLinux(configMachineId.value)
    backupLinuxVerify.value = {
      loading: false,
      report: res.data.report || res.data.error || '(无返回)',
      ok: !!res.data.success
    }
    showMsg(res.data.success ? '远端校验通过' : '远端校验失败', res.data.success ? 'success' : 'error')
  } catch (e) {
    backupLinuxVerify.value = { loading: false, report: e.response?.data?.error || e.message, ok: false }
    showMsg('远端校验失败: ' + (e.response?.data?.error || e.message), 'error')
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

async function importSSHHosts() {
  try {
    const res = await configApi.importSSHHosts(configMachineId.value)
    sshHostList.value = res.data.hosts || []
    // 同步刷新任务 tab 的主机列表
    if (selectedMachineId.value === configMachineId.value) {
      sshHosts.value = res.data.hosts || []
    }
    const count = res.data.count || 0
    let msg = `成功导入 ${count} 台主机`
    if (res.data.pushError) {
      msg += '（已保存到控制台，但回写 Agent 失败：' + res.data.pushError + '）'
    }
    showMsg(msg)
  } catch (e) {
    showMsg('导入失败: ' + e.message, 'error')
  }
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
async function downloadAgentPack(m) {
  try {
    const res = await machineApi.downloadPack(m.id)
    const blob = new Blob([res.data], { type: 'application/zip' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    const filename = res.headers['content-disposition']
      ? res.headers['content-disposition'].match(/filename="?(.+?)"?$/)?.[1] || `mutagen-agent-${m.name}.zip`
      : `mutagen-agent-${m.name}.zip`
    a.download = filename
    a.click()
    URL.revokeObjectURL(url)
  } catch (e) {
    showMsg('下载安装包失败: ' + e.message, 'error')
  }
}

watch(selectedMachineId, () => { currentPage.value = 1; loadTasks(); loadTaskHosts() })
watch(configMachineId, loadConfig)

onMounted(() => {
  if (loggedIn.value) {
    loadMachines()
    startMachinesPolling()
  }
  // 任务状态自动轮询（与 agent 10s 上报周期对齐），仅在任务页且已选中机器时拉取
  startTasksPolling()
})

onBeforeUnmount(() => {
  stopAllPolling()
  pendingTimers.forEach(clearTimeout)
  pendingTimers.clear()
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
.topbar {
  display: flex;
  justify-content: flex-end;
  align-items: center;
  gap: 8px;
  padding: 0 0 14px;
  margin-bottom: 16px;
  border-bottom: 1px solid #e5e7eb;
}
.section { background: #fff; padding: 24px; border-radius: 12px; box-shadow: 0 1px 3px rgba(0,0,0,0.08); }
.table-card { background: #fafafa; border-radius: 8px; padding: 16px; margin-top: 16px; border: 1px solid #eee; }
h2 { margin-bottom: 20px; color: #333; }
.task-count {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 22px;
  height: 22px;
  padding: 0 8px;
  margin-left: 8px;
  border-radius: 11px;
  background: #e5e7eb;
  color: #374151;
  font-size: 13px;
  font-weight: 600;
  line-height: 1;
  vertical-align: middle;
}
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
.sub-options { margin-left: 24px; padding-left: 16px; border-left: 2px solid #e5e7eb; }
.mode-input { margin-left: auto; width: 120px; padding: 6px 10px; border: 1px solid #ddd; border-radius: 4px; font-family: monospace; }

.host-table .readonly-cell { padding: 12px; color: #333; font-size: 13px; }
.host-table input { width: 100%; padding: 6px 8px; border: 1px solid #ddd; border-radius: 4px; box-sizing: border-box; }
.host-table .port-input { width: 70px; }
.host-actions { display: flex; gap: 12px; margin-top: 12px; align-items: center; }
.hint { margin-top: 10px; color: #888; font-size: 12px; line-height: 1.5; }
.verify-report { margin-top: 12px; padding: 12px; border-radius: 6px; font-family: monospace; font-size: 12px; white-space: pre-wrap; word-break: break-all; border: 1px solid #ddd; }
.verify-report.ok { background: #f0fdf4; border-color: #86efac; color: #166534; }
.verify-report.bad { background: #fef2f2; border-color: #fca5a5; color: #991b1b; }

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
.btn-small.btn-disabled,
.btn-small:disabled { background: #cbd5e1; color: #94a3b8; cursor: not-allowed; }
.btn-small.btn-disabled:hover,
.btn-small:disabled:hover { background: #cbd5e1; }
.btn-mini { padding: 6px 12px; font-size: 14px; margin-left: 8px; background: #94a3b8; }
.btn-mini:hover { background: #64748b; }

/* 同名任务警告：行高亮 + 名称旁标记 */
.row-warning { background: #fef3c7 !important; }
.row-warning:hover { background: #fde68a !important; }
.dup-mark { margin-left: 4px; color: #d97706; cursor: help; }

.table { width: 100%; border-collapse: collapse; margin-top: 16px; }
.table th, .table td { padding: 12px; text-align: left; border-bottom: 1px solid #eee; }
.table th { background: #f8fafc; color: #333; font-weight: 600; font-size: 14px; }
.token { font-family: monospace; font-size: 14px; color: #666; }

.badge { display: inline-block; padding: 6px 14px; border-radius: 12px; font-size: 14px; }
.badge.success { background: #dcfce7; color: #166534; }
.badge.gray { background: #f3f4f6; color: #6b7280; }

.error-cell { font-size: 14px; }
.error-text { color: #dc2626; cursor: help; border-bottom: 1px dotted #dc2626; }
.transition-problems-badge { color: #d97706; cursor: help; border-bottom: 1px dotted #d97706; margin-left: 6px; white-space: nowrap; }

.empty-state { text-align: center; color: #999; padding: 40px; }
.pagination { display: flex; align-items: center; gap: 12px; justify-content: center; padding: 12px 0; }
.pagination span { color: #666; font-size: 13px; }

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