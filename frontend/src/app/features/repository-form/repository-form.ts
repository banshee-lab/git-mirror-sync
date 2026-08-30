import { HttpClient } from '@angular/common/http';
import { Component, OnInit, inject, signal } from '@angular/core';
import { FormBuilder, FormGroup, ReactiveFormsModule, Validators } from '@angular/forms';
import { ActivatedRoute, Router, RouterLink } from '@angular/router';
import { firstValueFrom } from 'rxjs';
import { ConnectionTestResult, Repository, SSHKey } from '../../core/models';
import { getApiErrorMessage } from '../../core/http/api-error';
import { AlertBannerComponent } from '../../shared/alert-banner/alert-banner.component';

@Component({
  selector: 'app-repository-form',
  standalone: true,
  imports: [ReactiveFormsModule, AlertBannerComponent, RouterLink],
  template: `
    <div class="space-y-6">
      <!-- Header -->
      <div class="flex items-center gap-4">
        <a routerLink="/repositories" class="text-slate-400 hover:text-white transition-colors">
          <svg xmlns="http://www.w3.org/2000/svg" class="h-5 w-5" viewBox="0 0 20 20" fill="currentColor">
            <path fill-rule="evenodd" d="M9.707 14.707a1 1 0 01-1.414 0l-4-4a1 1 0 010-1.414l4-4a1 1 0 011.414 1.414L7.414 9H15a1 1 0 110 2H7.414l2.293 2.293a1 1 0 010 1.414z" clip-rule="evenodd"/>
          </svg>
        </a>
        <div>
          <h2 class="text-2xl font-semibold text-white">{{ isEdit ? 'Edit Repository' : 'Add Repository' }}</h2>
          <p class="text-sm text-slate-400">{{ isEdit ? 'Update the configuration for this sync mirror.' : 'Configure a new source-to-destination git sync mirror.' }}</p>
        </div>
      </div>

      <!-- Global alerts -->
      <app-alert-banner [message]="successMsg()" type="success" />
      <app-alert-banner [message]="errorMsg()" type="error" />

      @if (loadingRepo()) {
        <div class="rounded-2xl border border-slate-800 bg-slate-900 p-6 text-slate-400">Loading repository…</div>
      } @else {
        <form [formGroup]="form" (ngSubmit)="onSubmit()" class="space-y-6">

          <!-- General -->
          <section class="rounded-2xl border border-slate-800 bg-slate-900 p-5 space-y-4">
            <h3 class="text-base font-semibold text-white">General</h3>
            <div class="grid gap-4 md:grid-cols-2">
              <div>
                <label class="mb-1 block text-sm text-slate-300">Name <span class="text-rose-400">*</span></label>
                <input formControlName="name"
                  class="w-full rounded-xl border bg-slate-950 px-3 py-2 text-white focus:outline-none transition-colors"
                  [class.border-slate-700]="!fieldError('name')"
                  [class.focus:border-emerald-400]="!fieldError('name')"
                  [class.border-rose-500]="fieldError('name')"
                  placeholder="owner/repo" />
                @if (fieldError('name')) {
                  <p class="mt-1 text-xs text-rose-400">{{ fieldError('name') }}</p>
                }
              </div>
              <div class="flex items-center gap-3 pt-6">
                <label class="relative inline-flex items-center cursor-pointer">
                  <input type="checkbox" formControlName="enabled" class="sr-only peer">
                  <div class="w-11 h-6 bg-slate-700 rounded-full peer peer-checked:bg-emerald-500 transition-colors"></div>
                  <div class="absolute left-0.5 top-0.5 bg-white w-5 h-5 rounded-full transition-transform peer-checked:translate-x-5"></div>
                </label>
                <span class="text-sm text-slate-300">Enabled</span>
              </div>
              <div class="md:col-span-2">
                <label class="mb-1 block text-sm text-slate-300">Webhook Secret <span class="text-rose-400">*</span></label>
                <div class="flex gap-2">
                  <input formControlName="webhook_secret" type="text"
                    class="flex-1 rounded-xl border border-slate-700 bg-slate-950 px-3 py-2 text-white focus:border-emerald-400 focus:outline-none"
                    [class.border-slate-700]="!fieldError('webhook_secret')"
                    [class.border-rose-500]="fieldError('webhook_secret')"
                    placeholder="Leave blank to keep existing or auto-generate" />
                  <button type="button" (click)="generateSecret()"
                    class="rounded-xl border border-slate-600 bg-slate-800 px-3 py-2 text-sm text-slate-300 hover:bg-slate-700 hover:text-white transition-colors">
                    Generate
                  </button>
                </div>
                @if (fieldError('webhook_secret')) {
                  <p class="mt-1 text-xs text-rose-400">{{ fieldError('webhook_secret') }}</p>
                }
                <p class="mt-1 text-xs text-slate-500">Used to verify webhook payloads from the pull-side provider.</p>
              </div>
            </div>
          </section>

          <!-- Pull Source -->
          <section class="rounded-2xl border border-slate-800 bg-slate-900 p-5 space-y-4">
            <h3 class="text-base font-semibold text-white">Pull Source</h3>
            <div class="grid gap-4 md:grid-cols-2">
              <div class="md:col-span-2">
                <label class="mb-1 block text-sm text-slate-300">Pull URL <span class="text-rose-400">*</span></label>
                <input formControlName="pull_url"
                  class="w-full rounded-xl border bg-slate-950 px-3 py-2 text-white font-mono text-sm focus:outline-none transition-colors"
                  [class.border-slate-700]="!fieldError('pull_url')"
                  [class.border-rose-500]="fieldError('pull_url')"
                  placeholder="git@github.com:owner/repo.git" />
                @if (fieldError('pull_url')) {
                  <p class="mt-1 text-xs text-rose-400">{{ fieldError('pull_url') }}</p>
                }
              </div>
              <div>
                <div class="flex items-center justify-between mb-1">
                  <label class="block text-sm text-slate-300">Pull SSH Key <span class="text-rose-400">*</span></label>
                  <button type="button" (click)="showPullKeyPanel.set(!showPullKeyPanel())" class="text-xs text-sky-400 hover:text-sky-300 transition-colors">+ New key</button>
                </div>
                <select formControlName="pull_ssh_key_id"
                  class="w-full rounded-xl border bg-slate-950 px-3 py-2 text-white focus:outline-none transition-colors"
                  [class.border-slate-700]="!fieldError('pull_ssh_key_id')"
                  [class.border-rose-500]="fieldError('pull_ssh_key_id')">
                  <option value="">Select a key…</option>
                  @for (key of keys(); track key.id) {
                    <option [value]="key.id">{{ key.name }} ({{ key.key_type }})</option>
                  }
                </select>
                @if (fieldError('pull_ssh_key_id')) {
                  <p class="mt-1 text-xs text-rose-400">{{ fieldError('pull_ssh_key_id') }}</p>
                }
                @if (showPullKeyPanel()) {
                  <div class="mt-3 rounded-xl border border-slate-700 bg-slate-900/50 p-3 space-y-3" [formGroup]="pullKeyForm">
                    <input formControlName="name" placeholder="Key name (e.g. github-pull)" class="w-full rounded-xl border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-white focus:border-emerald-400 focus:outline-none" />
                    <select formControlName="key_type" class="w-full rounded-xl border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-white focus:border-emerald-400 focus:outline-none">
                      <option value="ed25519">ed25519</option>
                      <option value="rsa">rsa</option>
                    </select>
                    @if (pullKeyError()) {
                      <div class="text-xs text-rose-400">{{ pullKeyError() }}</div>
                    }
                    <div class="flex gap-2">
                      <button type="button" (click)="generateKey('pull')" [disabled]="pullKeyForm.invalid || generatingPullKey()" class="flex-1 rounded-xl bg-emerald-500 px-3 py-1.5 text-xs font-medium text-slate-950 hover:bg-emerald-400 disabled:opacity-50 transition-colors">
                        {{ generatingPullKey() ? 'Generating…' : 'Generate' }}
                      </button>
                      <button type="button" (click)="showPullKeyPanel.set(false)" class="rounded-xl border border-slate-600 px-3 py-1.5 text-xs text-slate-300 hover:text-white transition-colors">Cancel</button>
                    </div>
                  </div>
                }
              </div>
            </div>
            @if (testResult()?.pull) {
              <div class="flex items-center gap-2 rounded-xl border px-3 py-2 text-sm font-medium"
                [class.border-emerald-500/40]="testResult()!.pull.ok"
                [class.bg-emerald-500/10]="testResult()!.pull.ok"
                [class.text-emerald-300]="testResult()!.pull.ok"
                [class.border-rose-500/40]="!testResult()!.pull.ok"
                [class.bg-rose-500/10]="!testResult()!.pull.ok"
                [class.text-rose-300]="!testResult()!.pull.ok">
                @if (testResult()!.pull.ok) {
                  <svg xmlns="http://www.w3.org/2000/svg" class="h-4 w-4" viewBox="0 0 20 20" fill="currentColor"><path fill-rule="evenodd" d="M16.707 5.293a1 1 0 010 1.414l-8 8a1 1 0 01-1.414 0l-4-4a1 1 0 011.414-1.414L8 12.586l7.293-7.293a1 1 0 011.414 0z" clip-rule="evenodd"/></svg>
                } @else {
                  <svg xmlns="http://www.w3.org/2000/svg" class="h-4 w-4" viewBox="0 0 20 20" fill="currentColor"><path fill-rule="evenodd" d="M4.293 4.293a1 1 0 011.414 0L10 8.586l4.293-4.293a1 1 0 111.414 1.414L11.414 10l4.293 4.293a1 1 0 01-1.414 1.414L10 11.414l-4.293 4.293a1 1 0 01-1.414-1.414L8.586 10 4.293 5.707a1 1 0 010-1.414z" clip-rule="evenodd"/></svg>
                }
                {{ testResult()!.pull.message }}
              </div>
            }
          </section>

          <!-- Push Target -->
          <section class="rounded-2xl border border-slate-800 bg-slate-900 p-5 space-y-4">
            <h3 class="text-base font-semibold text-white">Push Target</h3>
            <div class="grid gap-4 md:grid-cols-2">
              <div class="md:col-span-2">
                <label class="mb-1 block text-sm text-slate-300">Push URL <span class="text-rose-400">*</span></label>
                <input formControlName="push_url"
                  class="w-full rounded-xl border bg-slate-950 px-3 py-2 text-white font-mono text-sm focus:outline-none transition-colors"
                  [class.border-slate-700]="!fieldError('push_url')"
                  [class.border-rose-500]="fieldError('push_url')"
                  placeholder="git@forgejo.example.com:owner/repo.git" />
                @if (fieldError('push_url')) {
                  <p class="mt-1 text-xs text-rose-400">{{ fieldError('push_url') }}</p>
                }
              </div>
              <div>
                <div class="flex items-center justify-between mb-1">
                  <label class="block text-sm text-slate-300">Push SSH Key <span class="text-rose-400">*</span></label>
                  <button type="button" (click)="showPushKeyPanel.set(!showPushKeyPanel())" class="text-xs text-sky-400 hover:text-sky-300 transition-colors">+ New key</button>
                </div>
                <select formControlName="push_ssh_key_id"
                  class="w-full rounded-xl border bg-slate-950 px-3 py-2 text-white focus:outline-none transition-colors"
                  [class.border-slate-700]="!fieldError('push_ssh_key_id')"
                  [class.border-rose-500]="fieldError('push_ssh_key_id')">
                  <option value="">Select a key…</option>
                  @for (key of keys(); track key.id) {
                    <option [value]="key.id">{{ key.name }} ({{ key.key_type }})</option>
                  }
                </select>
                @if (fieldError('push_ssh_key_id')) {
                  <p class="mt-1 text-xs text-rose-400">{{ fieldError('push_ssh_key_id') }}</p>
                }
                @if (showPushKeyPanel()) {
                  <div class="mt-3 rounded-xl border border-slate-700 bg-slate-900/50 p-3 space-y-3" [formGroup]="pushKeyForm">
                    <input formControlName="name" placeholder="Key name (e.g. forgejo-push)" class="w-full rounded-xl border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-white focus:border-emerald-400 focus:outline-none" />
                    <select formControlName="key_type" class="w-full rounded-xl border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-white focus:border-emerald-400 focus:outline-none">
                      <option value="ed25519">ed25519</option>
                      <option value="rsa">rsa</option>
                    </select>
                    @if (pushKeyError()) {
                      <div class="text-xs text-rose-400">{{ pushKeyError() }}</div>
                    }
                    <div class="flex gap-2">
                      <button type="button" (click)="generateKey('push')" [disabled]="pushKeyForm.invalid || generatingPushKey()" class="flex-1 rounded-xl bg-emerald-500 px-3 py-1.5 text-xs font-medium text-slate-950 hover:bg-emerald-400 disabled:opacity-50 transition-colors">
                        {{ generatingPushKey() ? 'Generating…' : 'Generate' }}
                      </button>
                      <button type="button" (click)="showPushKeyPanel.set(false)" class="rounded-xl border border-slate-600 px-3 py-1.5 text-xs text-slate-300 hover:text-white transition-colors">Cancel</button>
                    </div>
                  </div>
                }
              </div>
            </div>
            @if (testResult()?.push) {
              <div class="flex items-center gap-2 rounded-xl border px-3 py-2 text-sm font-medium"
                [class.border-emerald-500/40]="testResult()!.push.ok"
                [class.bg-emerald-500/10]="testResult()!.push.ok"
                [class.text-emerald-300]="testResult()!.push.ok"
                [class.border-rose-500/40]="!testResult()!.push.ok"
                [class.bg-rose-500/10]="!testResult()!.push.ok"
                [class.text-rose-300]="!testResult()!.push.ok">
                @if (testResult()!.push.ok) {
                  <svg xmlns="http://www.w3.org/2000/svg" class="h-4 w-4" viewBox="0 0 20 20" fill="currentColor"><path fill-rule="evenodd" d="M16.707 5.293a1 1 0 010 1.414l-8 8a1 1 0 01-1.414 0l-4-4a1 1 0 011.414-1.414L8 12.586l7.293-7.293a1 1 0 011.414 0z" clip-rule="evenodd"/></svg>
                } @else {
                  <svg xmlns="http://www.w3.org/2000/svg" class="h-4 w-4" viewBox="0 0 20 20" fill="currentColor"><path fill-rule="evenodd" d="M4.293 4.293a1 1 0 011.414 0L10 8.586l4.293-4.293a1 1 0 111.414 1.414L11.414 10l4.293 4.293a1 1 0 01-1.414 1.414L10 11.414l-4.293 4.293a1 1 0 01-1.414-1.414L8.586 10 4.293 5.707a1 1 0 010-1.414z" clip-rule="evenodd"/></svg>
                }
                {{ testResult()!.push.message }}
              </div>
            }
          </section>

          <!-- Action bar -->
          <div class="flex items-center gap-3 flex-wrap">
            <button type="button" (click)="testConnection()" [disabled]="testing()"
              class="rounded-xl border border-slate-600 bg-slate-800 px-4 py-2 text-sm font-medium text-slate-300 hover:bg-slate-700 hover:text-white transition-colors disabled:opacity-50">
              {{ testing() ? 'Testing…' : 'Test Connection' }}
            </button>
            <div class="flex-1"></div>
            <a routerLink="/repositories"
              class="rounded-xl border border-slate-700 px-4 py-2 text-sm font-medium text-slate-400 hover:text-white transition-colors">
              Cancel
            </a>
            <button type="submit" [disabled]="form.invalid || saving()"
              class="rounded-xl bg-emerald-500 px-5 py-2 text-sm font-medium text-slate-950 hover:bg-emerald-400 transition-colors disabled:opacity-50 disabled:cursor-not-allowed">
              {{ saving() ? 'Saving…' : (isEdit ? 'Update repository' : 'Create repository') }}
            </button>
          </div>

          @if (testError()) {
            <app-alert-banner [message]="testError()" type="error" />
          }
        </form>
      }
    </div>
  `,
})
export class RepositoryFormComponent implements OnInit {
  private readonly http = inject(HttpClient);
  private readonly fb = inject(FormBuilder);
  private readonly route = inject(ActivatedRoute);
  private readonly router = inject(Router);

  isEdit = false;
  repoId: number | null = null;

  readonly loadingRepo = signal(false);
  readonly saving = signal(false);
  readonly testing = signal(false);

  readonly successMsg = signal('');
  readonly errorMsg = signal('');
  readonly testError = signal('');
  readonly testResult = signal<ConnectionTestResult | null>(null);

  readonly keys = signal<SSHKey[]>([]);

  readonly pullKeyForm = this.fb.group({
    name: ['', [Validators.required, Validators.minLength(2), Validators.maxLength(100)]],
    key_type: ['ed25519', Validators.required]
  });
  readonly showPullKeyPanel = signal(false);
  readonly generatingPullKey = signal(false);
  readonly pullKeyError = signal('');

  readonly pushKeyForm = this.fb.group({
    name: ['', [Validators.required, Validators.minLength(2), Validators.maxLength(100)]],
    key_type: ['ed25519', Validators.required]
  });
  readonly showPushKeyPanel = signal(false);
  readonly generatingPushKey = signal(false);
  readonly pushKeyError = signal('');

  form: FormGroup = this.fb.group({
    name: ['', [Validators.required]],
    pull_url: ['', Validators.required],
    pull_ssh_key_id: ['', Validators.required],
    push_url: ['', Validators.required],
    push_ssh_key_id: ['', Validators.required],
    webhook_secret: ['', Validators.required],
    enabled: [true],
  });

  fieldError(field: string): string | null {
    const ctrl = this.form.get(field);
    if (!ctrl || !ctrl.touched || ctrl.valid) return null;
    if (ctrl.errors?.['required']) return 'This field is required.';
    if (ctrl.errors?.['minlength']) {
      return 'Must be at least ' + ctrl.errors['minlength'].requiredLength + ' characters.';
    }
    return 'Invalid value.';
  }

  async ngOnInit(): Promise<void> {
    await this.loadKeys();
    const id = this.route.snapshot.paramMap.get('id');
    if (id) {
      this.isEdit = true;
      this.repoId = parseInt(id, 10);
      await this.loadRepository(this.repoId);
    }
  }

  async onSubmit(): Promise<void> {
    this.form.markAllAsTouched();
    if (this.form.invalid) return;

    this.saving.set(true);
    this.successMsg.set('');
    this.errorMsg.set('');

    try {
      const val = this.form.value;
      const body: Record<string, unknown> = {
        name: val.name,
        pull_url: val.pull_url,
        pull_ssh_key_id: Number(val.pull_ssh_key_id),
        push_url: val.push_url,
        push_ssh_key_id: Number(val.push_ssh_key_id),
        enabled: val.enabled,
      };
      if (val.webhook_secret) {
        body['webhook_secret'] = val.webhook_secret;
      }

      if (this.isEdit && this.repoId) {
        await firstValueFrom(this.http.put<Repository>('/api/repositories/' + this.repoId, body));
        this.successMsg.set('Repository updated successfully.');
        await this.router.navigate(['/repositories']);
      } else {
        await firstValueFrom(this.http.post<Repository>('/api/repositories', body));
        this.successMsg.set('Repository created successfully.');
        await this.router.navigate(['/repositories']);
      }
    } catch (err) {
      this.errorMsg.set(getApiErrorMessage(err, 'Failed to save repository.'));
    } finally {
      this.saving.set(false);
    }
  }

  async testConnection(): Promise<void> {
    this.testing.set(true);
    this.testResult.set(null);
    this.testError.set('');

    try {
      const val = this.form.getRawValue();
      let url: string;
      let body: Record<string, unknown>;

      if (this.isEdit && this.repoId) {
        url = '/api/repositories/' + this.repoId + '/test-connection';
        body = {};
      } else {
        url = '/api/repositories/test-connection';
        body = {
          pull_url: val.pull_url,
          pull_ssh_key_id: Number(val.pull_ssh_key_id),
          push_url: val.push_url,
          push_ssh_key_id: Number(val.push_ssh_key_id),
        };
      }

      const result = await firstValueFrom(this.http.post<ConnectionTestResult>(url, body));
      this.testResult.set(result);
    } catch (err) {
      this.testError.set(getApiErrorMessage(err, 'Connection test failed.'));
    } finally {
      this.testing.set(false);
    }
  }

  generateSecret(): void {
    const chars = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789';
    const arr = new Uint8Array(32);
    crypto.getRandomValues(arr);
    const secret = Array.from(arr).map(b => chars[b % chars.length]).join('');
    this.form.get('webhook_secret')?.setValue(secret);
  }

  async generateKey(type: 'pull' | 'push'): Promise<void> {
    const isPull = type === 'pull';
    const form = isPull ? this.pullKeyForm : this.pushKeyForm;
    const generatingSignal = isPull ? this.generatingPullKey : this.generatingPushKey;
    const errorSignal = isPull ? this.pullKeyError : this.pushKeyError;
    const panelSignal = isPull ? this.showPullKeyPanel : this.showPushKeyPanel;
    const formControlName = isPull ? 'pull_ssh_key_id' : 'push_ssh_key_id';

    if (form.invalid) {
      form.markAllAsTouched();
      return;
    }

    generatingSignal.set(true);
    errorSignal.set('');

    try {
      const val = form.getRawValue();
      const newKey = await firstValueFrom(this.http.post<SSHKey>('/api/ssh-keys', {
        name: val.name?.trim(),
        key_type: val.key_type
      }));

      this.keys.update(keys => [...keys, newKey]);
      this.form.get(formControlName)?.setValue(newKey.id);

      form.reset({ name: '', key_type: 'ed25519' });
      panelSignal.set(false);
    } catch (err) {
      errorSignal.set(getApiErrorMessage(err, 'Failed to generate SSH key.'));
    } finally {
      generatingSignal.set(false);
    }
  }

  private async loadKeys(): Promise<void> {
    try {
      const keys = await firstValueFrom(this.http.get<SSHKey[]>('/api/ssh-keys'));
      this.keys.set(keys ?? []);
    } catch {
      this.keys.set([]);
    }
  }

  private async loadRepository(id: number): Promise<void> {
    this.loadingRepo.set(true);
    try {
      const repo = await firstValueFrom(this.http.get<Repository>('/api/repositories/' + id));
      this.form.patchValue({
        name: repo.name,
        pull_url: repo.pull_url_raw,
        pull_ssh_key_id: repo.pull_ssh_key_id,
        push_url: repo.push_url_raw,
        push_ssh_key_id: repo.push_ssh_key_id,
        enabled: repo.enabled,
        webhook_secret: '',
      });
    } catch (err) {
      this.errorMsg.set(getApiErrorMessage(err, 'Failed to load repository.'));
    } finally {
      this.loadingRepo.set(false);
    }
  }
}
