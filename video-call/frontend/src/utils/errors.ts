export const DEFAULT_ERROR_MESSAGE = 'Something went wrong'

export function getErrorMessage(error: unknown): string {
  if (error instanceof Error && error.message) {
    return error.message
  }
  if (typeof error === 'string' && error) {
    return error
  }
  return DEFAULT_ERROR_MESSAGE
}
