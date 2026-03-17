<script>
  import { page } from '$app/stores';
</script>

<svelte:head>
  <title>{$page.status} — Checkout</title>
</svelte:head>

<div class="error-wrap">
  <div class="error-card">
    <div class="icon">
      {#if $page.status === 404}
        <svg viewBox="0 0 52 52" fill="none">
          <circle cx="26" cy="26" r="26" fill="#f6f9fc" stroke="#e3e8ee" stroke-width="1.5"/>
          <text x="26" y="34" text-anchor="middle" font-size="22" font-family="Arial" fill="#697386">?</text>
        </svg>
      {:else}
        <svg viewBox="0 0 52 52" fill="none">
          <circle cx="26" cy="26" r="26" fill="#fff0f3" stroke="#fbc8d4" stroke-width="1.5"/>
          <path d="M26 16v12M26 32v2" stroke="#df1b41" stroke-width="2.5" stroke-linecap="round"/>
        </svg>
      {/if}
    </div>
    <h1>{$page.status === 404 ? 'Link not found' : 'Something went wrong'}</h1>
    <p>{$page.error?.message || 'An unexpected error occurred.'}</p>
    <div class="powered-by">
      <svg viewBox="0 0 18 18" fill="none" width="16" height="16">
        <rect width="18" height="18" rx="4" fill="#00b574"/>
        <path d="M5 9l2.5 2.5L13 6" stroke="white" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
      </svg>
      <span>Powered by <strong>OpenPay</strong></span>
    </div>
  </div>
</div>

<style>
  .error-wrap {
    min-height: 100vh;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 2rem;
  }
  .error-card {
    background: #fff;
    border-radius: 12px;
    box-shadow: 0 4px 16px rgba(0,0,0,.08);
    padding: 3rem 2.5rem;
    max-width: 400px;
    width: 100%;
    text-align: center;
  }
  .icon { margin-bottom: 1.25rem; }
  .icon svg { width: 52px; height: 52px; display: inline-block; }
  h1 { font-size: 1.25rem; font-weight: 700; margin-bottom: .6rem; font-family: 'Inter', sans-serif; }
  p  { color: #697386; font-size: .9rem; line-height: 1.6; margin-bottom: 1.75rem; font-family: 'Inter', sans-serif; }
  .powered-by {
    display: inline-flex;
    align-items: center;
    gap: .4rem;
    font-size: .78rem;
    color: #697386;
    font-family: 'Inter', sans-serif;
  }
  .powered-by strong { color: #1a1f36; }
</style>
