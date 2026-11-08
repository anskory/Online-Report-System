<!--
  SideNav.vue - 左侧菜单（框架层）
  数据来自 /api/auth/me 的 menus（目录 M 带 children，页面 C 用 path 跳转）。

  两点刻意设计：
  1) 路径统一走 normalizeMenuPath 归一：库里可能还存着 /ui/xxx.html 这类旧值，
     直接丢给 router 会匹配不到路由并被兜底重定向，表现为「点菜单没反应」。
  2) 不使用 el-menu 的 router 模式，改为 @select 显式跳转：
     可以先校验目标路由是否存在，路径配错时给明确提示，而不是静默失败。
-->
<template>
  <el-menu
    class="rs-menu"
    :default-active="active"
    :default-openeds="openedDirs"
    :collapse="collapsed"
    :collapse-transition="false"
    @select="onSelect"
  >
    <template v-for="m in menus" :key="m.id">
      <el-sub-menu v-if="childrenOf(m).length" :index="'dir-' + m.id">
        <template #title>
          <el-icon><component :is="iconOf(m.icon)" /></el-icon>
          <span class="rs-menu-text">{{ m.menuName }}</span>
        </template>
        <el-menu-item v-for="c in childrenOf(m)" :key="c.id" :index="link(c)">
          <el-icon><component :is="iconOf(c.icon || m.icon)" /></el-icon>
          <template #title>{{ c.menuName }}</template>
        </el-menu-item>
      </el-sub-menu>

      <el-menu-item v-else :index="link(m)">
        <el-icon><component :is="iconOf(m.icon)" /></el-icon>
        <template #title>{{ m.menuName }}</template>
      </el-menu-item>
    </template>
  </el-menu>
</template>

<script>
import * as Icons from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import { RS, normalizeMenuPath } from '@/lib/rs.js'

/** 兜底重定向路由的 path 片段，用于判断「这个地址没有对应页面」 */
const FALLBACK_MARK = 'pathMatch'

/**
 * 不在左侧导航里出现的页面。
 * 报表配置已改为从「报表中心」页内按钮进入（页内有返回按钮），
 * 放在这里而不是改库里的 sys_menu：不动数据、不打扰菜单管理，改回来只需删一项。
 */
const HIDDEN_PATHS = ['/config']

export default {
  name: 'SideNav',
  props: {
    collapsed: { type: Boolean, default: false },
  },
  computed: {
    /** 只保留「有可见子项」或「自身路径有效」的菜单，避免渲染出点了没反应的死项 */
    menus() {
      return (RS.me.menus || [])
        .filter((m) => !HIDDEN_PATHS.includes(this.link(m)))
        .filter((m) => this.childrenOf(m).length > 0 || this.link(m))
    },
    /** 当前高亮项：不在菜单里的页面（如 /view）用 meta.activeMenu 回指菜单 */
    active() {
      const meta = this.$route.meta || {}
      return normalizeMenuPath(meta.activeMenu) || this.$route.path
    },
    /** 当前页所属目录默认展开，否则刷新后子项是收起的，高亮看不见 */
    openedDirs() {
      const out = []
      this.menus.forEach((m) => {
        if (this.childrenOf(m).some((c) => this.link(c) === this.active)) out.push('dir-' + m.id)
      })
      return out
    },
  },
  methods: {
    /** 菜单项的可跳转路径（已归一化；目录或空路径返回 ''） */
    link(m) {
      return normalizeMenuPath(m && m.path)
    },
    childrenOf(m) {
      return (m.children || [])
        .filter((c) => this.link(c))
        .filter((c) => !HIDDEN_PATHS.includes(this.link(c)))
    },
    /** 菜单里存的是 Element Plus 图标名（Document/EditPen…），取不到时回退 Document */
    iconOf(name) {
      return (name && Icons[name]) || Icons.Document
    },
    onSelect(index) {
      if (!index || index.indexOf('dir-') === 0) return
      const target = normalizeMenuPath(index)
      const route = this.$router.resolve(target)
      const hit = route.matched && route.matched.length
      // 兜底路由（/:pathMatch(.*)*）说明前端没有这个页面对应的路由
      if (!hit || route.matched.some((r) => String(r.path).indexOf(FALLBACK_MARK) >= 0)) {
        ElMessage.warning('菜单路径「' + target + '」没有对应的页面，请检查菜单管理里的路径配置')
        return
      }
      if (this.$route.fullPath === target) return
      this.$router.push(target).catch(() => {})
    },
  },
}
</script>
