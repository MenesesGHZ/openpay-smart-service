<script>
  import { page } from '$app/stores';
</script>

<svelte:head>
  <title>Checkout</title>
</svelte:head>

<div class="not-found">
  <div class="card">
    <div class="logo">
      <svg width="32" height="32" viewBox="0 0 32 32" fill="none">
        <circle cx="16" cy="16" r="16" fill="#00b574"/>
        <path d="M10 16l4 4 8-8" stroke="white" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"/>
      </svg>
    </div>
    <h1>OpenPay Checkout</h1>
    <p>This page is used to process payments via subscription links. If you received a payment link, please use the full URL provided to you.</p>
  </div>
</div>

<style>
  .not-found {
    min-height: 100vh;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 2rem;
  }
  .card {
    background: var(--white);
    border-radius: var(--radius);
    box-shadow: var(--shadow-md);
    padding: 3rem 2.5rem;
    max-width: 420px;
    width: 100%;
    text-align: center;
  }
  .logo { margin-bottom: 1.5rem; }
  h1 { font-size: 1.25rem; font-weight: 600; margin-bottom: .75rem; }
  p { color: var(--text-secondary); font-size: .9rem; line-height: 1.6; }
</style>
