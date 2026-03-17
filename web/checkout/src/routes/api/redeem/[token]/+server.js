import { json, error } from '@sveltejs/kit';
import { API_BASE_URL } from '$env/static/private';

/** POST /api/redeem/:token — proxy to backend RedeemSubscriptionLink */
export async function POST({ params, request }) {
  const body = await request.json();

  const res = await fetch(
    `${API_BASE_URL}/v1/subscription-links/${params.token}/redeem`,
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    }
  );

  const data = await res.json().catch(() => ({}));

  if (!res.ok) {
    return json(data, { status: res.status });
  }

  return json(data);
}
