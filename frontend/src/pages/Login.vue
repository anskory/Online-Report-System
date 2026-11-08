<!--
  Login.vue - 登录 - 在线报表系统
  模板与逻辑均由迁移前的 login.html（in-DOM 模板）与内联脚本原样搬入，业务逻辑未改动。
-->
<template>
  <div class="login-wrap">
    <!-- 左侧品牌区：窄屏自动隐藏（见 style.css 媒体查询） -->
    <div class="login-hero">
      <div class="login-glow"></div>
      <div class="login-brand">
        <img src="/favicon.svg" alt="logo">
        <b>在线报表系统</b>
      </div>
      <h1>让每一张报表<br><em>更快、更准、更省心</em></h1>
      <p>统一管理数据源与查询报表，零代码配置查询条件，一键导出 Excel，生产数据触手可及。</p>
      <div class="login-feats">
        <span><i>✓</i> 多数据源接入：Oracle / MySQL / PostgreSQL / SQL Server</span>
        <span><i>✓</i> 零代码报表配置，SQL 参数自动装配</span>
        <span><i>✓</i> 字典联动、权限管控、Excel 一键导出</span>
      </div>
    </div>

    <!-- 右侧登录卡片：原登录逻辑不变 -->
    <div class="login-side">
      <div class="login-card">
        <img class="login-logo" src="/favicon.svg" alt="logo">
        <h2>欢迎回来 👋</h2>
        <div class="sub">请使用系统账号登录</div>
        <el-form @submit.prevent="doLogin">
          <el-form-item>
            <el-input v-model="username" placeholder="用户名" size="large">
              <template #prefix><i-user style="width:16px;height:16px"></i-user></template>
            </el-input>
          </el-form-item>
          <el-form-item>
            <el-input v-model="password" type="password" placeholder="密码" size="large" show-password>
              <template #prefix><i-lock style="width:16px;height:16px"></i-lock></template>
            </el-input>
          </el-form-item>
          <el-button type="primary" size="large" style="width:100%" :loading="loading" native-type="submit">登 录</el-button>
        </el-form>
        <div class="muted" style="margin-top:14px;text-align:center">初始账号 admin / admin123，请登录后修改</div>
      </div>
    </div>
  </div>
</template>

<script>
import { ElMessage } from 'element-plus'
import { RS } from '@/lib/rs.js'

export default {
  data: function () {
    return { username: '', password: '', loading: false };
  },
  created: function () {
    // 已登录直接进入（按角色菜单决定落地页）
    if (!RS.token()) return;
    RS.loadMe().then(function () {
      RS.navTo(RS.homePath());
    }).catch(function () { /* 令牌失效：留在登录页 */ });
  },
  methods: {
    doLogin: function () {
      var self = this;
      // 防重入：请求进行中直接忽略（防回车/按钮重复触发导致重复登录、日志双写）
      if (this.loading) return;
      if (!this.username || !this.password) {
        ElMessage.warning('请输入用户名和密码');
        return;
      }
      this.loading = true;
      RS.post('/api/auth/login', { username: this.username, password: this.password })
        .then(function (data) {
          RS.setToken(data.accessToken, data.user);
          ElMessage.success('登录成功');
          var back = RS.qs('back');
          // 只接受站内绝对路径（拒绝 //evil.com 这类协议相对地址，避免开放重定向）
          if (back && back.charAt(0) === '/' && back.charAt(1) !== '/') { RS.navTo(back); return; }
          // 按角色菜单决定落地页（无报表权限的用户不会被弹回报表首页）
          return RS.loadMe().then(function () {
            RS.navTo(RS.homePath());
          }).catch(function () {
            RS.navTo('/reports');
          });
        })
        .catch(function (e) { RS.alertErr(e); })
        .finally(function () { self.loading = false; });
    }
  }
}
</script>
