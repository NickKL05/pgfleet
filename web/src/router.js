import { createRouter, createWebHistory } from 'vue-router'
import FleetOverview from './views/FleetOverview.vue'
import TenantDetail from './views/TenantDetail.vue'

const routes = [
  { path: '/', name: 'overview', component: FleetOverview },
  { path: '/tenant/:schema', name: 'tenant', component: TenantDetail, props: true },
]

export const router = createRouter({
  history: createWebHistory(),
  routes,
})
