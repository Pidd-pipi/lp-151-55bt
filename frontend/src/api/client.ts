import axios from 'axios'

export interface ApiResponse<T = unknown> {
  code: number
  message: string
  data: T
}

export class ApiError extends Error {
  code?: number
  status?: number

  constructor(message: string, code?: number, status?: number) {
    super(message)
    this.name = 'ApiError'
    this.code = code
    this.status = status
  }
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

export async function request<T>(method: 'get' | 'post' | 'delete', url: string, data?: unknown): Promise<T> {
  try {
    const response = await client.request<ApiResponse<T>>({
      method,
      url,
      ...(method === 'get' ? { params: data } : { data }),
    })
    if (response.data.code !== 0) {
      throw new ApiError(response.data.message || '请求失败', response.data.code, response.status)
    }
    return response.data.data
  } catch (err) {
    // 后端返回的业务错误（如 409 已撤回）转换为带状态码的 ApiError，便于页面提示
    if (axios.isAxiosError(err)) {
      const body = err.response?.data as ApiResponse | undefined
      if (body?.message) {
        throw new ApiError(body.message, body.code, err.response?.status)
      }
    }
    throw err
  }
}
