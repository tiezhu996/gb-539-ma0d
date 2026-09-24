import {Component, inject} from '@angular/core';
import {NgFor, NgIf, DatePipe} from '@angular/common';
import {FormsModule} from '@angular/forms';
import {AppStore} from '../stores/app.store';
import {readingApi} from '../api/readings';
import {ApiError} from '../api/client';
import {DryingCurveChartComponent} from '../components/common/DryingCurveChart';
import type {MoistureReading} from '../types/entities';

@Component({standalone:true,imports:[FormsModule,NgFor,NgIf,DatePipe,DryingCurveChartComponent],template:`
<div class="page-head"><div><p class="eyebrow">03 / READINGS</p><h1>含水率工作台</h1><p class="description">导入读数、识别异常，逐条采纳或排除后再进入曲线计算。</p></div><button class="button primary" (click)="showForm=!showForm">{{showForm?'取消':'＋ 导入读数'}}</button></div>
<form *ngIf="showForm" class="workspace-panel modal-form" (ngSubmit)="importReading()"><h2>导入含水率读数</h2><label>工艺批次<select name="lot" [(ngModel)]="draft.timber_lot_id" required><option *ngFor="let lot of store.lots()" [value]="lot.id">{{lot.lot_code}} · {{lot.species}}</option></select></label><div class="form-grid"><label>采样位置<select name="position" [(ngModel)]="draft.sample_position"><option value="surface">表层</option><option value="core">中心</option></select></label><label>含水率 %<input name="moisture" type="number" step="0.1" [(ngModel)]="draft.moisture_pct" required></label></div><div class="form-grid"><label>干球 °C<input name="dry" type="number" step="0.1" [(ngModel)]="draft.dry_bulb_c" required></label><label>湿球 °C<input name="wet" type="number" step="0.1" [(ngModel)]="draft.wet_bulb_c" required></label></div><button class="button primary" type="submit">导入一条读数</button><p class="form-error">{{error}}</p></form>
<div class="curve-card"><div><p class="eyebrow">MOISTURE PROFILE</p><h2>{{average()}}<small>% 平均含水率</small></h2><p>仅统计正常与已采纳读数；待处理异常被排除在曲线外。</p></div><kc-drying-curve [values]="values()"/></div>
<section *ngIf="pending().length" class="workspace-panel review-panel"><div class="panel-head"><div><h2>异常读数复核 · {{pending().length}} 条待处理</h2><p>仍有待处理异常时曲线仿真不会生成计划。请逐条选择采纳（参与计算）或排除（保留历史、不参与），并写明理由。</p></div><span class="count">状态按记录显示</span></div><div class="table-wrap"><table><thead><tr><th>批次 / 位置</th><th>含水率 / 干湿球</th><th>测量时间</th><th>标记原因</th><th *ngIf="canReview()">复核操作</th><th *ngIf="!canReview()">处理状态</th></tr></thead><tbody><tr *ngFor="let row of pending()"><td><strong>{{lotCode(row.timber_lot_id)}}</strong><small>{{row.sample_position}} · v{{row.version}}</small></td><td>{{row.moisture_pct}}%<small>{{row.dry_bulb_c}}°C / {{row.wet_bulb_c}}°C</small></td><td>{{row.measured_at | date:'yyyy-MM-dd HH:mm'}}</td><td class="reason-cell">{{row.quality_note}}</td><td *ngIf="canReview()"><input name="reason-{{row.id}}" class="review-reason" [(ngModel)]="reasons[row.id]" placeholder="复核理由（至少 5 个字）"><div class="review-actions"><button class="button tiny good" [disabled]="busy[row.id]===true || !reasonReady(row.id)" (click)="review(row,'adopted')">采纳</button><button class="button tiny muted" [disabled]="busy[row.id]===true || !reasonReady(row.id)" (click)="review(row,'excluded')">排除</button></div><small class="row-error">{{errors[row.id]}}</small></td><td *ngIf="!canReview()"><span class="state warm">{{statusLabel(row)}}</span></td></tr></tbody></table></div></section>
<section class="workspace-panel"><div class="panel-head"><div><h2>读数记录</h2><p>采纳参与计算；排除与作废保留历史但不参与；处理状态逐条可见。</p></div><span class="count">{{rows().length}} 条</span></div><div class="table-wrap"><table><thead><tr><th>批次 / 位置</th><th>含水率 / 干湿球</th><th>测量时间</th><th>质量 / 处理状态</th><th>复核结论</th></tr></thead><tbody><tr *ngFor="let row of rows()"><td><strong>{{lotCode(row.timber_lot_id)}}</strong><small>{{row.sample_position}}</small></td><td>{{row.moisture_pct}}%<small>{{row.dry_bulb_c}}°C / {{row.wet_bulb_c}}°C</small></td><td>{{row.measured_at | date:'yyyy-MM-dd HH:mm'}}</td><td><span class="state" [class.warm]="row.reading_quality==='flagged'" [class.good]="row.reading_quality==='accepted'">{{row.reading_quality}}</span><small *ngIf="row.review_state">{{statusLabel(row)}}</small><small *ngIf="row.reading_quality==='voided'">voided · v{{row.version}}</small></td><td><small *ngIf="row.review_reason" class="reason-cell">{{row.review_reason}} — {{row.reviewed_by}}</small><small *ngIf="!row.review_reason">—</small></td></tr></tbody></table></div></section>`})
export class ReadingsPageComponent {
  readonly store=inject(AppStore);
  showForm=false; error=''; draft={timber_lot_id:'',sample_position:'core',moisture_pct:28,dry_bulb_c:56,wet_bulb_c:42};
  reasons:Record<string,string>={}; errors:Record<string,string>={}; busy:Record<string,boolean>={};
  rows=()=>this.store.readings();
  pending=()=>this.rows().filter(row=>row.reading_quality==='flagged' && (row.review_state==='pending'||row.review_state===''));
  values=()=>this.rows().filter(row=>row.reading_quality!=='voided' && (row.reading_quality==='accepted'||row.review_state==='adopted')).map(x=>x.moisture_pct);
  average=()=>{const values=this.values();return values.length?(values.reduce((sum,value)=>sum+value,0)/values.length).toFixed(1):'—'};
  canReview=()=>this.store.role()==='quality_analyst'||this.store.role()==='admin';
  lotCode=(id:string)=>this.store.lots().find(lot=>lot.id===id)?.lot_code || id.slice(0,8);
  reasonReady=(row:MoistureReading)=>(this.reasons[row.id]||'').trim().length>=5;
  statusLabel=(row:MoistureReading)=>row.reading_quality==='voided'?'已作废':({pending:'待处理',adopted:'已采纳',excluded:'已排除'} as Record<string,string>)[row.review_state]||'待处理';
  async importReading(){try{this.draft.timber_lot_id ||= this.store.lots()[0]?.id || '';await readingApi.import({timber_lot_id:this.draft.timber_lot_id,readings:[{sample_position:this.draft.sample_position,moisture_pct:this.draft.moisture_pct,dry_bulb_c:this.draft.dry_bulb_c,wet_bulb_c:this.draft.wet_bulb_c}]});this.showForm=false;this.error='';await this.store.refresh()}catch(error){this.error=error instanceof Error?error.message:'导入失败'}}
  async review(row:MoistureReading, decision:'adopted'|'excluded'){
    const reason=(this.reasons[row.id]||'').trim();
    this.errors[row.id]=''; this.busy[row.id]=true;
    try{
      await readingApi.review(row.id, decision, reason, row.version);
      delete this.reasons[row.id];
      await this.store.refresh();
    }catch(error){
      if(error instanceof ApiError && error.status===409){this.errors[row.id]='该记录已被另一位分析师先完成处理或版本已更新，列表已刷新';await this.store.refresh()}
      else{this.errors[row.id]=error instanceof Error?error.message:'复核提交失败'}
    }finally{this.busy[row.id]=false}
  }
}
