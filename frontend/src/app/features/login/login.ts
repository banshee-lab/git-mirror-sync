import { Component, OnInit } from '@angular/core';
import { Router } from '@angular/router';
import { AuthService } from '../../core/auth/auth.service';

@Component({
  selector: 'app-login',
  standalone: true,
  template: `
    <section class="flex min-h-[70vh] items-center justify-center">
      <div class="w-full max-w-md rounded-2xl border border-slate-700 bg-slate-900 p-8 shadow-2xl shadow-slate-950/50">
        <div class="mb-6 text-center">
          <div class="mx-auto mb-4 flex h-14 w-14 items-center justify-center rounded-full bg-emerald-500/15 text-2xl text-emerald-400">
            <i class="fa-solid fa-shield-halved"></i>
          </div>
          <h1 class="text-2xl font-semibold text-white">Forgejo Sync</h1>
          <p class="mt-2 text-sm text-slate-400">Sign in to manage mirror repos and SSH keys.</p>
        </div>

        <button
          type="button"
          (click)="auth.login()"
          class="flex w-full items-center justify-center gap-3 rounded-xl bg-emerald-500 px-4 py-3 font-medium text-slate-950 transition hover:bg-emerald-400"
        >
          <i class="fa-solid fa-right-to-bracket"></i>
          Login using {{ auth.providerName() }}
        </button>
      </div>
    </section>
  `,
})
export class LoginComponent implements OnInit {
  constructor(
    public readonly auth: AuthService,
    private readonly router: Router,
  ) {}

  async ngOnInit(): Promise<void> {
    await this.auth.loadConfig();
    const user = await this.auth.refreshUser();
    if (user) {
      await this.router.navigateByUrl('/repositories');
    }
  }
}
