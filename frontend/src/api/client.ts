import type {AffectedReading} from '../types/entities';
const base = '/api/v1';
export class ApiError extends Error {
  readonly status:number; readonly code:string; readonly affected:AffectedReading[];
  constructor(message:string, status:number, code:string, affected:AffectedReading[]){ super(message); this.name='ApiError'; this.status=status; this.code=code; this.affected=affected; }
}
export async function request<T>(path:string, options:RequestInit = {}):Promise<T>{ const headers = new Headers(options.headers); headers.set('Content-Type','application/json'); const token = localStorage.getItem('kilncurve_token'); if(token) headers.set('Authorization', `Bearer ${token}`); const response = await fetch(`${base}${path}`, {...options, headers}); const body = await response.json().catch(()=>({})); if(!response.ok) throw new ApiError(body?.error?.message || `请求失败 (${response.status})`, response.status, body?.error?.code || '', body?.error?.affected || []); return body.data as T; }
