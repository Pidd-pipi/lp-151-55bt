import axios from 'axios'

export interface ApiResponse<T = unknown> {
  code: number
  message: string
  data: T
}

export const client = axios.create({
  baseURL: '/api/v1',
  timeout: 15000,
})

client.interceptors.request.use((config) => {
  const token = localStorage.getItem('gbtreehole_token')
  if (token) {
    config.headers.Authorization = `Bearer ${token}`
  }
  return config
})

client.interceptors.response.use(
  (response) => response,
  (error) => {
    if (error.response?.status === 401) {
      localStorage.removeItem('gbtreehole_token')
    }
    return Promise.reject(error)
  },
)

export function extractError(error: unknown): string {
  if (typeof error === 'object' && error !== null && 'response' in error) {
    const data = (error as { response?: { data?: ApiResponse } }).response?.data
    if (data?.message) return data.message
  }
  if (error instanceof Error && error.message) return error.message
  return '请求失败'
}

export async function request<T>(method: 'get' | 'post' | 'delete', url: string, data?: unknown): Promise<T> {
  const response = await client.request<ApiResponse<T>>({
    method,
    url,
    ...(method === 'get' ? { params: data } : { data }),
  })
  if (response.data.code !== 0) {
    throw new Error(response.data.message || '请求失败')
  }
  return response.data.data
}
