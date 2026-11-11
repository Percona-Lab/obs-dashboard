<template>
  <div role="status" data-testid="release-loading" class="flex items-center gap-2 text-[13px] text-text-muted">
    <span aria-hidden="true" class="inline-block h-4 w-4 animate-spin rounded-full border-2 border-border-strong border-t-brand-purple motion-reduce:animate-none"></span>
    <span><slot /></span>
  </div>
</template>
