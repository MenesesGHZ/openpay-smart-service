<script>
  import { onMount } from 'svelte';
  import {
    PUBLIC_OPENPAY_MERCHANT_ID,
    PUBLIC_OPENPAY_PUBLIC_KEY,
    PUBLIC_OPENPAY_SANDBOX,
  } from '$env/static/public';

  export let data;
  const { token, info } = data;

  // ── State ─────────────────────────────────────────────────────────────────
  let cardNumber    = '';
  let expiry        = '';
  let cvc           = '';
  let nameOnCard    = info.memberName || '';
  let email         = info.memberEmail || '';

  let state         = 'idle'; // idle | processing | success | error
  let errorMsg      = '';
  let deviceSessionId = '';
  let openpayReady  = false;

  // ── Derived ───────────────────────────────────────────────────────────────
  $: alreadyRedeemed = info.status === 'redeemed';
  $: alreadyExpired  = info.status === 'expired' || info.status === 'cancelled';

  $: formattedAmount = (() => {
    return (info.amount / 100).toLocaleString('es-MX', {
      style: 'currency', currency: info.currency || 'MXN', minimumFractionDigits: 2
    });
  })();

  $: currencyCode = (info.currency || 'MXN').toUpperCase();

  $: paymentDescription = info.description || 'One-time payment';

  $: cardBrand = (() => {
    const n = cardNumber.replace(/\s/g, '');
    if (/^4/.test(n))      return 'visa';
    if (/^5[1-5]/.test(n)) return 'mastercard';
    if (/^3[47]/.test(n))  return 'amex';
    return 'unknown';
  })();

  // ── OpenPay init ──────────────────────────────────────────────────────────
  onMount(() => {
    const interval = setInterval(() => {
      if (typeof window.OpenPay !== 'undefined') {
        clearInterval(interval);
        window.OpenPay.setId(PUBLIC_OPENPAY_MERCHANT_ID);
        window.OpenPay.setApiKey(PUBLIC_OPENPAY_PUBLIC_KEY);
        window.OpenPay.setSandboxMode(PUBLIC_OPENPAY_SANDBOX === 'true');
        deviceSessionId = window.OpenPay.deviceData.setup();
        openpayReady = true;
      }
    }, 100);
    return () => clearInterval(interval);
  });

  // ── Input formatting ──────────────────────────────────────────────────────
  function onCardNumberInput(e) {
    const raw    = e.target.value.replace(/\D/g, '').slice(0, 16);
    const chunks = raw.match(/.{1,4}/g) || [];
    cardNumber   = chunks.join(' ');
    e.target.value = cardNumber;
  }

  function onExpiryInput(e) {
    const raw = e.target.value.replace(/\D/g, '').slice(0, 4);
    expiry = raw.length > 2 ? raw.slice(0, 2) + ' / ' + raw.slice(2) : raw;
    e.target.value = expiry;
  }

  function onExpiryKeydown(e) {
    if (e.key === 'Backspace' && expiry.endsWith(' / ')) {
      e.preventDefault();
      expiry = expiry.slice(0, -3);
    }
  }

  function onCvcInput(e) {
    cvc = e.target.value.replace(/\D/g, '').slice(0, 4);
    e.target.value = cvc;
  }

  // ── Submit ────────────────────────────────────────────────────────────────
  async function submit() {
    if (state === 'processing') return;
    errorMsg = '';

    const rawCard = cardNumber.replace(/\s/g, '');
    if (rawCard.length < 15) { errorMsg = 'Please enter a valid card number.'; return; }
    if (!expiry.includes('/')) { errorMsg = 'Please enter the card expiry date.'; return; }
    if (cvc.length < 3)      { errorMsg = 'Please enter the CVC.'; return; }
    if (!nameOnCard.trim())  { errorMsg = 'Please enter the name on the card.'; return; }

    const [mm, yy] = expiry.split('/').map(s => s.trim());

    if (!openpayReady) {
      errorMsg = 'Payment processor is still loading. Please try again in a moment.';
      return;
    }

    state = 'processing';

    // Step 1 – tokenize card via OpenPay JS SDK
    const tokenResult = await new Promise((resolve) => {
      window.OpenPay.token.create(
        {
          card_number:      rawCard,
          holder_name:      nameOnCard,
          expiration_year:  yy,
          expiration_month: mm,
          cvv2:             cvc,
        },
        (res) => resolve({ ok: true,  tokenId: res.data.id }),
        (err) => resolve({ ok: false, message: err.data?.description || 'Card tokenization failed.' })
      );
    });

    if (!tokenResult.ok) {
      state    = 'error';
      errorMsg = tokenResult.message;
      return;
    }

    // Step 2 – redeem payment link
    try {
      const res = await fetch(`/api/pay/${token}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          token_id:          tokenResult.tokenId,
          device_session_id: deviceSessionId,
        }),
      });

      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        throw new Error(body.message || `Server error ${res.status}`);
      }

      state = 'success';
    } catch (err) {
      state    = 'error';
      errorMsg = err.message || 'Something went wrong. Please try again.';
    }
  }
</script>

<svelte:head>
  <title>{paymentDescription ? `Pay — ${paymentDescription}` : 'Checkout'}</title>
  <script src="https://js.openpay.mx/openpay.v1.min.js"></script>
  <script src="https://js.openpay.mx/openpay-data.v1.min.js"></script>
</svelte:head>

<!-- ── Already redeemed / expired ──────────────────────────────────────────── -->
{#if alreadyRedeemed}
  <div class="solo-wrap">
    <div class="solo-card">
      <div class="check-icon success">
        <svg viewBox="0 0 24 24" fill="none">
          <circle cx="12" cy="12" r="12" fill="#00b574"/>
          <path d="M7 12l3.5 3.5L17 8" stroke="#fff" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
        </svg>
      </div>
      <h2>Already paid</h2>
      <p>This payment link has already been used.</p>
    </div>
  </div>

{:else if alreadyExpired}
  <div class="solo-wrap">
    <div class="solo-card">
      <div class="check-icon error">
        <svg viewBox="0 0 24 24" fill="none">
          <circle cx="12" cy="12" r="12" fill="#df1b41"/>
          <path d="M8 8l8 8M16 8l-8 8" stroke="#fff" stroke-width="2" stroke-linecap="round"/>
        </svg>
      </div>
      <h2>Link expired</h2>
      <p>This payment link has expired or been cancelled. Please request a new one.</p>
    </div>
  </div>

<!-- ── Success screen ─────────────────────────────────────────────────────── -->
{:else if state === 'success'}
  <div class="solo-wrap">
    <div class="solo-card">
      <div class="check-icon success">
        <svg viewBox="0 0 24 24" fill="none">
          <circle cx="12" cy="12" r="12" fill="#00b574"/>
          <path d="M7 12l3.5 3.5L17 8" stroke="#fff" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
        </svg>
      </div>
      <h2>Payment complete!</h2>
      <p>Your payment of <strong>{formattedAmount}</strong> was processed successfully. A receipt will be sent to <strong>{email}</strong>.</p>
    </div>
  </div>

<!-- ── Checkout form ──────────────────────────────────────────────────────── -->
{:else}
  <div class="layout">
    <!-- Left panel — order summary -->
    <aside class="summary">
      <div class="summary-inner">
        <div class="merchant">
          <div class="merchant-logo" aria-hidden="true">
            <svg width="14" height="14" viewBox="0 0 14 14" fill="none">
              <path d="M2.5 7l3 3 6-6" stroke="white" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round"/>
            </svg>
          </div>
          <span class="merchant-name">OpenPay</span>
        </div>

        <div class="plan-info">
          <p class="plan-label">Payment for</p>
          <h1 class="plan-name">{paymentDescription}</h1>
          <p class="plan-description">One-time · Secure checkout</p>
          <p class="plan-price">
            <span class="price-amount">{formattedAmount}</span>
            <span class="price-currency">{currencyCode}</span>
          </p>
        </div>

        <div class="summary-divider"></div>

        <div class="summary-line">
          <span>Subtotal</span>
          <span>{formattedAmount}</span>
        </div>
        <div class="summary-line total">
          <span>Total due today</span>
          <span>{formattedAmount}</span>
        </div>

        <div class="summary-footer">
          <div class="powered-by">
            <svg class="op-icon" viewBox="0 0 16 16" fill="none">
              <rect width="16" height="16" rx="4" fill="var(--brand)"/>
              <path d="M4 8l2.75 2.75L12 5.5" stroke="white" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
            </svg>
            <span>Powered by <strong>OpenPay</strong></span>
          </div>
        </div>
      </div>
    </aside>

    <!-- Right panel — payment form -->
    <main class="form-panel">
      <div class="form-inner">
        <!-- Contact info -->
        <section class="form-section">
          <h2 class="section-label">Contact information</h2>
          <div class="field">
            <label for="email">Email</label>
            <input
              id="email"
              type="email"
              bind:value={email}
              placeholder="you@example.com"
              autocomplete="email"
            />
          </div>
        </section>

        <!-- Payment details -->
        <section class="form-section">
          <h2 class="section-label">Payment details</h2>

          <div class="field">
            <label for="cardNumber">Card number</label>
            <div class="card-input-wrap">
              <input
                id="cardNumber"
                type="text"
                inputmode="numeric"
                placeholder="1234 1234 1234 1234"
                autocomplete="cc-number"
                value={cardNumber}
                on:input={onCardNumberInput}
              />
              <span class="card-brand">
                {#if cardBrand === 'visa'}
                  <svg viewBox="0 0 48 16" fill="none" class="brand-icon">
                    <text x="0" y="13" font-family="Arial" font-size="14" font-weight="700" fill="#1A1F71">VISA</text>
                  </svg>
                {:else if cardBrand === 'mastercard'}
                  <svg viewBox="0 0 32 20" class="brand-icon">
                    <circle cx="12" cy="10" r="10" fill="#EB001B"/>
                    <circle cx="20" cy="10" r="10" fill="#F79E1B"/>
                    <path d="M16 3.5a10 10 0 010 13A10 10 0 0116 3.5z" fill="#FF5F00"/>
                  </svg>
                {:else if cardBrand === 'amex'}
                  <svg viewBox="0 0 48 16" fill="none" class="brand-icon">
                    <text x="0" y="13" font-family="Arial" font-size="11" font-weight="700" fill="#2E77BC">AMEX</text>
                  </svg>
                {/if}
              </span>
            </div>
          </div>

          <div class="field-row">
            <div class="field">
              <label for="expiry">Expiration date</label>
              <input
                id="expiry"
                type="text"
                inputmode="numeric"
                placeholder="MM / YY"
                autocomplete="cc-exp"
                value={expiry}
                on:input={onExpiryInput}
                on:keydown={onExpiryKeydown}
              />
            </div>
            <div class="field">
              <label for="cvc">
                Security code
                <span class="cvc-hint" title="3 or 4 digit code on the back of your card">
                  <svg width="14" height="14" viewBox="0 0 14 14" fill="none">
                    <circle cx="7" cy="7" r="6.5" stroke="#9ba3af"/>
                    <text x="7" y="11" text-anchor="middle" font-size="9" fill="#9ba3af" font-family="Arial">?</text>
                  </svg>
                </span>
              </label>
              <input
                id="cvc"
                type="text"
                inputmode="numeric"
                placeholder="CVC"
                autocomplete="cc-csc"
                value={cvc}
                on:input={onCvcInput}
              />
            </div>
          </div>

          <div class="field">
            <label for="nameOnCard">Name on card</label>
            <input
              id="nameOnCard"
              type="text"
              bind:value={nameOnCard}
              placeholder="Full name"
              autocomplete="cc-name"
            />
          </div>
        </section>

        {#if errorMsg}
          <div class="error-banner" role="alert">
            <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
              <circle cx="8" cy="8" r="7.5" stroke="#df1b41"/>
              <path d="M8 4.5v4M8 10.5v1" stroke="#df1b41" stroke-width="1.5" stroke-linecap="round"/>
            </svg>
            {errorMsg}
          </div>
        {/if}

        <button
          class="btn-pay"
          on:click={submit}
          disabled={state === 'processing'}
          aria-live="polite"
        >
          {#if state === 'processing'}
            <span class="spinner"></span>
            Processing…
          {:else}
            Pay {formattedAmount}
          {/if}
        </button>

        <p class="terms">
          By completing this payment you authorise OpenPay to charge your card for the amount shown above.
        </p>

        <div class="powered-by-mobile">
          <svg class="op-icon" viewBox="0 0 16 16" fill="none">
            <rect width="16" height="16" rx="4" fill="var(--brand)"/>
            <path d="M4 8l2.75 2.75L12 5.5" stroke="white" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
          </svg>
          <span>Powered by <strong>OpenPay</strong></span>
        </div>
      </div>
    </main>
  </div>
{/if}

<style>
  /* ── Layout ──────────────────────────────────────────────────────────────── */
  .layout {
    min-height: 100vh;
    display: grid;
    grid-template-columns: 1fr 1fr;
  }

  /* ── Summary — dark left panel ───────────────────────────────────────────── */
  .summary {
    background: var(--panel-bg);
    display: flex;
    justify-content: flex-end;
    position: relative;
  }
  .summary::after {
    content: '';
    position: absolute;
    inset-block: 0;
    right: 0;
    width: 1px;
    background: var(--panel-border);
  }
  .summary-inner {
    max-width: 440px;
    width: 100%;
    padding: 4.5rem 3rem 3rem 2.5rem;
    display: flex;
    flex-direction: column;
  }

  .merchant {
    display: flex;
    align-items: center;
    gap: .625rem;
    margin-bottom: 3rem;
  }
  .merchant-logo {
    width: 28px; height: 28px;
    background: var(--brand);
    border-radius: 7px;
    display: flex; align-items: center; justify-content: center;
    flex-shrink: 0;
  }
  .merchant-logo svg { display: block; }
  .merchant-name {
    font-size: .875rem;
    font-weight: 500;
    color: var(--panel-text);
    letter-spacing: -.01em;
  }

  .plan-label {
    font-size: .6875rem;
    font-weight: 500;
    text-transform: uppercase;
    letter-spacing: .1em;
    color: var(--panel-muted);
    margin-bottom: .5rem;
  }
  .plan-name {
    font-size: 1.25rem;
    font-weight: 500;
    color: var(--panel-text);
    margin-bottom: .375rem;
    letter-spacing: -.02em;
    line-height: 1.3;
  }
  .plan-description {
    font-size: .8125rem;
    color: var(--panel-muted);
    letter-spacing: -.005em;
    margin-bottom: 1.25rem;
    line-height: 1.5;
  }
  .plan-price {
    display: flex;
    align-items: baseline;
    gap: .35rem;
    flex-wrap: wrap;
  }
  .price-amount {
    font-size: 2.75rem;
    font-weight: 600;
    color: var(--panel-text);
    letter-spacing: -.04em;
    line-height: 1;
  }
  .price-currency {
    font-size: .75rem;
    font-weight: 500;
    color: var(--panel-muted);
    letter-spacing: .04em;
    text-transform: uppercase;
    background: rgba(255,255,255,.08);
    border: 1px solid rgba(255,255,255,.1);
    border-radius: 4px;
    padding: .15rem .4rem;
    align-self: flex-end;
    margin-bottom: .35rem;
    font-family: var(--font-mono);
  }

  .summary-divider {
    height: 1px;
    background: var(--panel-border);
    margin: 2.5rem 0 1.5rem;
  }
  .summary-line {
    display: flex;
    justify-content: space-between;
    font-size: .8125rem;
    color: var(--panel-muted);
    margin-bottom: .625rem;
    letter-spacing: -.005em;
  }
  .summary-line.total {
    font-size: .875rem;
    font-weight: 500;
    color: var(--panel-text);
    margin-top: .5rem;
  }

  .summary-footer {
    margin-top: auto;
    padding-top: 3rem;
  }
  .powered-by {
    display: flex;
    align-items: center;
    gap: .5rem;
    font-size: .75rem;
    color: var(--panel-muted);
    letter-spacing: -.005em;
  }
  .op-icon { width: 16px; height: 16px; display: block; flex-shrink: 0; }
  .powered-by strong { color: rgba(255,255,255,.6); font-weight: 500; }

  /* ── Form panel ───────────────────────────────────────────────────────────── */
  .form-panel {
    background: #fff;
    display: flex;
    justify-content: flex-start;
  }
  .form-inner {
    max-width: 420px;
    width: 100%;
    padding: 4.5rem 2.5rem 3rem 3rem;
  }

  .form-section { margin-bottom: 1.75rem; }
  .section-label {
    font-size: .6875rem;
    font-weight: 500;
    text-transform: uppercase;
    letter-spacing: .1em;
    color: var(--zinc-400);
    margin-bottom: .875rem;
  }

  .field { margin-bottom: .875rem; }
  .field label {
    display: flex;
    align-items: center;
    gap: .3rem;
    font-size: .8125rem;
    font-weight: 400;
    color: var(--zinc-500);
    margin-bottom: .375rem;
    letter-spacing: -.005em;
  }
  .field input {
    width: 100%;
    padding: .5625rem .75rem;
    border: 1px solid var(--zinc-200);
    border-radius: var(--radius-sm);
    font-size: .9375rem;
    font-family: var(--font);
    color: var(--zinc-950);
    background: #fff;
    outline: none;
    transition: border-color .12s ease, box-shadow .12s ease;
    appearance: none;
    letter-spacing: -.01em;
  }
  .field input::placeholder { color: var(--zinc-400); }
  .field input:hover:not(:focus) { border-color: var(--zinc-300); }
  .field input:focus {
    border-color: var(--zinc-700);
    box-shadow: 0 0 0 3px var(--input-ring);
  }

  .card-input-wrap { position: relative; }
  .card-input-wrap input { padding-right: 3rem; }
  .card-brand {
    position: absolute;
    right: .75rem; top: 50%;
    transform: translateY(-50%);
    pointer-events: none;
    display: flex; align-items: center;
  }
  .brand-icon { width: 30px; height: 18px; }

  .field-row {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: .75rem;
  }
  .cvc-hint { cursor: help; display: flex; color: var(--zinc-400); }

  .error-banner {
    display: flex;
    align-items: flex-start;
    gap: .5rem;
    background: var(--error-bg);
    border: 1px solid var(--error-border);
    border-radius: var(--radius-sm);
    color: var(--error);
    font-size: .8125rem;
    line-height: 1.5;
    padding: .75rem .875rem;
    margin-bottom: 1.125rem;
  }
  .error-banner svg { flex-shrink: 0; margin-top: 1px; }

  /* Pay button — uses brand green to visually distinguish from subscription */
  .btn-pay {
    width: 100%;
    background: var(--btn-bg);
    color: var(--btn-text);
    border: none;
    border-radius: var(--radius-sm);
    padding: .6875rem 1.25rem;
    font-size: .9375rem;
    font-weight: 500;
    font-family: var(--font);
    letter-spacing: -.01em;
    cursor: pointer;
    display: flex;
    align-items: center;
    justify-content: center;
    gap: .5rem;
    transition: background .12s ease, opacity .12s ease, transform .08s ease;
    margin-bottom: 1rem;
    line-height: 1.5;
  }
  .btn-pay:hover:not(:disabled) { background: var(--btn-hover); }
  .btn-pay:active:not(:disabled) { transform: scale(.99); }
  .btn-pay:disabled { opacity: .5; cursor: not-allowed; }

  .spinner {
    width: 14px; height: 14px;
    border: 1.5px solid rgba(255,255,255,.3);
    border-top-color: #fff;
    border-radius: 50%;
    animation: spin .65s linear infinite;
    flex-shrink: 0;
  }
  @keyframes spin { to { transform: rotate(360deg); } }

  .terms {
    font-size: .75rem;
    color: var(--zinc-400);
    line-height: 1.6;
    text-align: center;
    margin-bottom: 1.5rem;
    letter-spacing: -.005em;
  }

  .powered-by-mobile {
    display: none;
    align-items: center;
    justify-content: center;
    gap: .4rem;
    font-size: .75rem;
    color: var(--zinc-400);
  }
  .powered-by-mobile strong { color: var(--zinc-600); font-weight: 500; }

  /* ── Solo states ─────────────────────────────────────────────────────────── */
  .solo-wrap {
    min-height: 100vh;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 2rem;
    background: var(--zinc-50);
  }
  .solo-card {
    background: #fff;
    border: 1px solid var(--zinc-200);
    border-radius: var(--radius-lg);
    padding: 2.5rem 2rem;
    max-width: 400px;
    width: 100%;
    text-align: center;
  }
  .check-icon { display: inline-block; margin-bottom: 1.125rem; }
  .check-icon svg { width: 48px; height: 48px; display: block; }
  .solo-card h2 {
    font-size: 1.125rem;
    font-weight: 600;
    letter-spacing: -.02em;
    margin-bottom: .5rem;
    color: var(--zinc-900);
  }
  .solo-card p {
    color: var(--zinc-500);
    font-size: .875rem;
    line-height: 1.65;
    margin-bottom: .4rem;
  }

  /* ── Responsive ──────────────────────────────────────────────────────────── */
  @media (max-width: 768px) {
    .layout {
      grid-template-columns: 1fr;
      grid-template-rows: auto 1fr;
    }
    .summary::after { display: none; }
    .summary {
      border-bottom: 1px solid var(--panel-border);
      justify-content: center;
    }
    .summary-inner { padding: 2.5rem 1.5rem 2rem; max-width: 100%; }
    .plan-name  { font-size: 1.125rem; }
    .price-amount { font-size: 2.25rem; }
    .summary-footer { display: none; }
    .form-panel { justify-content: center; }
    .form-inner { padding: 2rem 1.5rem; max-width: 100%; }
    .powered-by-mobile { display: flex; }
  }
</style>
