import axios from 'axios'

const client = axios.create({ baseURL: '/api/v1', timeout: 30000 })

client.interceptors.request.use((config) => {
  const token = localStorage.getItem('easyavr_token')
  if (token) {
    config.headers = config.headers ?? {}
    config.headers.Authorization = `Bearer ${token}`
  }
  return config
})

client.interceptors.response.use(
  (resp) => resp,
  (error) => {
    if (error.response?.status === 401) {
      localStorage.removeItem('easyavr_token')
      if (location.hash !== '#/login') location.hash = '#/login'
    }
    return Promise.reject(error)
  },
)

export default client
