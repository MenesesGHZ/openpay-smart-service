import { error } from '@sveltejs/kit';
import { API_BASE_URL } from '$env/static/private';

export async function load({ params, fetch }) {
  const res = await fetch(`${API_BASE_URL}/v1/payment-links/${params.token}/info`);

  if (res.status === 404) {
    throw error(404, 'This payment link was not found or has already been used.');
  }
  if (!res.ok) {
    const body = await res.text();
    throw error(500, `Unable to load checkout: ${body}`);
  }

  const info = await res.json();
  return { token: params.token, info };
}
