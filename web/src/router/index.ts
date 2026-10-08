import { createRouter, createWebHashHistory } from 'vue-router'
import { useAuthStore } from '../stores/auth'

const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    { path: '/login', name: 'login', component: () => import('../views/Login.vue'), meta: { public: true } },
    {
      path: '/',
      component: () => import('../layouts/MainLayout.vue'),
      children: [
        { path: '', redirect: '/dashboard' },
        { path: 'dashboard', name: 'dashboard', component: () => import('../views/Dashboard.vue') },
        { path: 'devices', name: 'devices', component: () => import('../views/DeviceList.vue') },
        { path: 'gb', name: 'gb', component: () => import('../views/GbDevices.vue') },
        { path: 'ga1400', name: 'ga1400', component: () => import('../views/Ga1400.vue') },
        { path: 'gb35114', name: 'gb35114', component: () => import('../views/Gb35114.vue') },
        { path: 'live', name: 'live', component: () => import('../views/LiveView.vue') },
        { path: 'multiscreen', name: 'multiscreen', component: () => import('../views/MultiScreen.vue') },
        { path: 'recordings', name: 'recordings', component: () => import('../views/Recordings.vue') },
        { path: 'snapshots', name: 'snapshots', component: () => import('../views/Snapshots.vue') },
        { path: 'resources', name: 'resources', component: () => import('../views/ResourceCenter.vue') },
        { path: 'ai/providers', name: 'ai-providers', component: () => import('../views/AiProviders.vue') },
        { path: 'ai/tasks', name: 'ai-tasks', component: () => import('../views/AiTasks.vue') },
        { path: 'ai/events', name: 'ai-events', component: () => import('../views/AiEvents.vue') },
        { path: 'search', name: 'search', component: () => import('../views/Search.vue') },
        { path: 'notifications', name: 'notifications', component: () => import('../views/Notifications.vue') },
        { path: 'cluster', name: 'cluster', component: () => import('../views/Cluster.vue') },
        { path: 'apikeys', name: 'apikeys', component: () => import('../views/ApiKeys.vue') },
        { path: 'users', name: 'users', component: () => import('../views/Users.vue') },
      ],
    },
  ],
})

router.beforeEach((to) => {
  const auth = useAuthStore()
  if (!to.meta.public && !auth.isAuthenticated) return { name: 'login' }
  if (to.name === 'login' && auth.isAuthenticated) return { name: 'dashboard' }
  return true
})

export default router
