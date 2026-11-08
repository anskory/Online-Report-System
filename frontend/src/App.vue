<!--
  应用根组件：只做一件事——渲染路由出口。
  框架（侧栏/顶栏）在 layout/AppLayout.vue，页面在 pages/*.vue，二者互不感知。
-->
<template>
  <router-view />
</template>

<script>
export default {
  name: 'App',
}
</script>
