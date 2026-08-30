import { HttpClient } from '@angular/common/http';
import { Injectable, signal } from '@angular/core';
import { firstValueFrom } from 'rxjs';

export const AUTH_LOGIN_URL = '/auth/login';
export const APP_LOGIN_ROUTE = '/login';

export interface AuthConfigResponse {
  provider_name: string;
  login_url: string;
}

export interface MeResponse {
  sub: string;
  email?: string;
  name?: string;
}

@Injectable({ providedIn: 'root' })
export class AuthService {
  readonly providerName = signal('PocketID');
  readonly currentUser = signal<MeResponse | null>(null);

  constructor(private readonly http: HttpClient) {}

  async loadConfig(): Promise<void> {
    const config = await firstValueFrom(this.http.get<AuthConfigResponse>('/auth/config'));
    this.providerName.set(config.provider_name || 'PocketID');
  }

  async refreshUser(): Promise<MeResponse | null> {
    try {
      const user = await firstValueFrom(this.http.get<MeResponse>('/api/me'));
      this.currentUser.set(user);
      return user;
    } catch {
      this.currentUser.set(null);
      return null;
    }
  }

  login(): void {
    window.location.href = AUTH_LOGIN_URL;
  }

  async logout(): Promise<void> {
    await firstValueFrom(this.http.post('/auth/logout', {}));
    this.currentUser.set(null);
    window.location.href = APP_LOGIN_ROUTE;
  }
}
