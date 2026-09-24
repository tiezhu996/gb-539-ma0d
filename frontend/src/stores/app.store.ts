import {inject, Injectable, NgZone, signal} from '@angular/core';
import {auditApi, type AuditEvent} from '../api/audit';
import {authApi} from '../api/auth';
import {kilnApi} from '../api/kilns';
import {lotApi} from '../api/lots';
import {readingApi} from '../api/readings';
import {scheduleApi} from '../api/schedules';
import type {DryingKiln, TimberLot, MoistureReading, DryingSchedule} from '../types/entities';

@Injectable({providedIn: 'root'})
export class AppStore {
  private readonly zone = inject(NgZone);
  readonly kilns = signal<DryingKiln[]>([]);
  readonly lots = signal<TimberLot[]>([]);
  readonly readings = signal<MoistureReading[]>([]);
  readonly schedules = signal<DryingSchedule[]>([]);
  readonly audits = signal<AuditEvent[]>([]);
  readonly role = signal<string>(localStorage.getItem('kilncurve_role') || '');
  readonly loading = signal(false);
  readonly error = signal('');

  setRole(role:string):void { localStorage.setItem('kilncurve_role', role); this.role.set(role); }
  clearRole():void { localStorage.removeItem('kilncurve_role'); this.role.set(''); }

  async refresh(): Promise<void> {
    this.loading.set(true); this.error.set('');
    // The audit list is admin/auditor only; a 403 there must not hide the
    // operational data an analyst or engineer is allowed to see.
    const [kilns, lots, readings, schedules, identity, auditResult] = await Promise.all([
      kilnApi.list(), lotApi.list(), readingApi.list(), scheduleApi.list(), authApi.me().catch(()=>null),
      auditApi.list().then(value=>({value})).catch((error:{status?:number})=>({error})),
    ]);
    this.zone.run(() => {
      this.kilns.set(kilns); this.lots.set(lots); this.readings.set(readings); this.schedules.set(schedules);
      if (identity) this.setRole(identity.role);
      if ('value' in auditResult) this.audits.set(auditResult.value);
      else if (auditResult.error?.status !== 403) this.error.set('暂时无法读取工艺数据');
      this.loading.set(false);
    });
  }
}
