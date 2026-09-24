import {request} from './client'; import type {MoistureReading} from '../types/entities';
export const readingApi = {
  list:(lotId='')=>request<MoistureReading[]>(`/readings${lotId?`?lot_id=${encodeURIComponent(lotId)}`:''}`),
  import:(input:any)=>request<MoistureReading[]>('/readings/import',{method:'POST',body:JSON.stringify({...input,readings:input.readings?.map((row:any)=>({...row,measured_at:row.measured_at||new Date().toISOString()}))})}),
  review:(id:string, decision:'adopted'|'excluded', reason:string, version:number)=>request<MoistureReading>(`/readings/${id}/review`,{method:'POST',body:JSON.stringify({decision,reason,version})}),
};
