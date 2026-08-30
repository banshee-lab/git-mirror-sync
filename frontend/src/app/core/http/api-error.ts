import { HttpErrorResponse } from '@angular/common/http';
import { ApiErrorResponse } from '../models';

export function getApiErrorMessage(err: unknown, fallback = 'An unexpected error occurred.'): string {
  if (!err) {
    return fallback;
  }

  if (err instanceof HttpErrorResponse) {
    const errorBody = err.error as ApiErrorResponse | string | null;
    if (typeof errorBody === 'object' && errorBody !== null) {
      if (errorBody.error?.message) {
        return errorBody.error.message;
      }
      if (errorBody.message) {
        return errorBody.message;
      }
    }
    if (typeof errorBody === 'string' && errorBody.trim().length > 0) {
      return errorBody;
    }
    if (err.statusText && err.statusText !== 'OK') {
      return `${err.statusText} (${err.status})`;
    }
  }

  if (err instanceof Error && err.message) {
    return err.message;
  }

  return fallback;
}
