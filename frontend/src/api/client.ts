const base = '/api/v1';
export class ApiError extends Error {
  readonly status:number;
  readonly code:string;
  readonly details:Record<string, unknown>;
  constructor(status:number, code:string, message:string, details:Record<string, unknown>={}) {
    super(message);
    this.name='ApiError';
    this.status=status;
    this.code=code;
    this.details=details;
  }
}
export async function request<T>(path:string, options:RequestInit = {}):Promise<T>{ const headers = new Headers(options.headers); headers.set('Content-Type','application/json'); const token = localStorage.getItem('kilncurve_token'); if(token) headers.set('Authorization', `Bearer ${token}`); const response = await fetch(`${base}${path}`, {...options, headers}); const body = await response.json().catch(()=>({})); if(!response.ok) throw new ApiError(response.status, body?.error?.code||'request_failed', body?.error?.message || `请求失败 (${response.status})`, body?.error||{}); return body.data as T; }
