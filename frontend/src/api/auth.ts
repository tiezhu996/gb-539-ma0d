import {request} from './client';
export interface AuthIdentity { id:string; email:string; role:string }
export const authApi = { me:()=>request<AuthIdentity>('/auth/me') };
