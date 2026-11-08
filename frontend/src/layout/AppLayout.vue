<!--
  AppLayout.vue - 全局框架层（框架是框架）
  ┌──────────┬────────────────────────────┐
  │ 品牌      │ 折叠按钮 页面标题   用户菜单  │
  │ 左侧菜单  │────────────────────────────│
  │ （可折叠）│ <router-view> 页面内容      │
  └──────────┴────────────────────────────┘
  侧栏折叠状态存 localStorage；窗口宽度 < 992px 时自动折叠，避免小屏挤占内容区。
-->
<template>
  <div class="rs-layout">
    <aside class="rs-aside" :class="{ 'is-collapsed': collapsed }">
      <div class="rs-brand" @click="goHome" :title="collapsed ? '在线报表系统' : ''">
        <img class="rs-logo" src="/favicon.svg" alt="logo">
        <span class="rs-brand-text" v-show="!collapsed">在线报表系统</span>
      </div>
      <el-scrollbar class="rs-menu-wrap">
        <side-nav :collapsed="collapsed" />
      </el-scrollbar>
    </aside>

    <div class="rs-main">
      <header class="rs-topbar">
        <span class="rs-collapse" @click="toggle" :title="collapsed ? '展开菜单' : '收起菜单'">
          <el-icon>
            <Expand v-if="collapsed" />
            <Fold v-else />
          </el-icon>
        </span>
        <span class="rs-crumb">{{ title }}</span>
        <span class="rs-topbar-right">
          <!-- 明暗主题切换：状态由 lib/theme.js 管理，持久化到 localStorage -->
          <span class="rs-theme-btn" :title="theme === 'dark' ? '切换亮色模式' : '切换暗色模式'" @click="onToggleTheme">
            <el-icon><Sunny v-if="theme === 'dark'" /><Moon v-else /></el-icon>
          </span>
          <el-dropdown trigger="click" @command="onCommand">
            <span class="rs-user">
              <el-icon class="rs-user-icon"><User /></el-icon>
              <span class="rs-user-name">{{ userName }}</span>
              <el-icon class="rs-user-caret"><ArrowDown /></el-icon>
            </span>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item disabled>{{ roleText }}</el-dropdown-item>
                <el-dropdown-item divided command="logout">退出登录</el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </span>
      </header>

      <main class="rs-content">
        <router-view v-slot="{ Component }">
          <!-- out-in 切换模式：旧页先淡出、新页再淡入，页面都是单根节点可安全过渡 -->
          <transition name="rs-fade" mode="out-in">
            <component :is="Component" :key="$route.fullPath" />
          </transition>
        </router-view>
      </main>
    </div>
  </div>
</template>

<script>
import { Fold, Expand, User, ArrowDown, Sunny, Moon } from '@element-plus/icons-vue'
import { RS } from '@/lib/rs.js'
import { currentTheme, toggleTheme } from '@/lib/theme.js'
import SideNav from './SideNav.vue'

/** 折叠状态持久化键：跨页面/刷新后保持用户上次的选择 */
const COLLAPSE_KEY = 'rs_sidebar_collapsed'

export default {
  name: 'AppLayout',
  components: { SideNav, Fold, Expand, User, ArrowDown, Sunny, Moon },
  data() {
    return { collapsed: false, theme: currentTheme() }
  },
  computed: {
    title() {
      return (this.$route.meta && this.$route.meta.title) || ''
    },
    userName() {
      const u = RS.me.user || {}
      return u.displayName || u.username || '未登录'
    },
    roleText() {
      const roles = RS.me.roles || []
      if (roles.length) return roles.map((r) => r.roleName).join(' / ')
      return RS.isAdmin() ? '超级管理员' : '普通用户'
    },
  },
  created() {
    this.collapsed = localStorage.getItem(COLLAPSE_KEY) === '1'
    this.onResize()
    window.addEventListener('resize', this.onResize)
  },
  beforeUnmount() {
    window.removeEventListener('resize', this.onResize)
  },
  methods: {
    /** 窄屏自动折叠：只为不挤占内容区，不改写用户的持久化偏好 */
    onResize() {
      if (window.innerWidth < 992) this.collapsed = true
    },
    toggle() {
      this.collapsed = !this.collapsed
      localStorage.setItem(COLLAPSE_KEY, this.collapsed ? '1' : '0')
    },
    /** 切换明暗主题（lib/theme.js 负责持久化与应用） */
    onToggleTheme() {
      this.theme = toggleTheme()
    },
    goHome() {
      this.$router.push('/reports').catch(() => {})
    },
    onCommand(cmd) {
      if (cmd === 'logout') RS.logout()
    },
  },
}
</script>
