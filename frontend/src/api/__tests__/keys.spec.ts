import type { AxiosInstance } from "axios";
import { beforeEach, describe, expect, it, vi } from "vitest";

describe("keys api contract", () => {
	beforeEach(() => {
		vi.resetModules();
		localStorage.clear();
	});

	it("create sends granted_group_ids and default_group_id without forcing active-group switch payload", async () => {
		const keysAPI = await import("@/api/keys");
		const clientModule = await import("@/api/client");
		const apiClient = clientModule.apiClient as AxiosInstance;

		const adapter = vi.fn().mockResolvedValue({
			status: 200,
			data: {
				code: 0,
				data: { id: 1, name: "k" },
			},
			headers: {},
			config: {},
			statusText: "OK",
		});
		apiClient.defaults.adapter = adapter;

		await keysAPI.create(
			"multi-key",
			10,
			undefined,
			undefined,
			undefined,
			undefined,
			undefined,
			undefined,
			{
				granted_group_ids: [10, 11],
				default_group_id: 10,
			},
		);

		const req = adapter.mock.calls[0][0];
		const payload = JSON.parse(req.data);
		expect(req.url).toBe("/keys");
		expect(req.method).toBe("post");
		expect(payload).toMatchObject({
			name: "multi-key",
			group_id: 10,
			granted_group_ids: [10, 11],
			default_group_id: 10,
		});
	});

	it("update allows fallback default_group_id + granted_group_ids without extra active-group field", async () => {
		const keysAPI = await import("@/api/keys");
		const clientModule = await import("@/api/client");
		const apiClient = clientModule.apiClient as AxiosInstance;

		const adapter = vi.fn().mockResolvedValue({
			status: 200,
			data: {
				code: 0,
				data: { id: 1, name: "k2" },
			},
			headers: {},
			config: {},
			statusText: "OK",
		});
		apiClient.defaults.adapter = adapter;

		await keysAPI.update(1, {
			default_group_id: 11,
			granted_group_ids: [10, 11],
		});

		const req = adapter.mock.calls[0][0];
		const payload = JSON.parse(req.data);
		expect(req.url).toBe("/keys/1");
		expect(req.method).toBe("put");
		expect(payload).toEqual({
			default_group_id: 11,
			granted_group_ids: [10, 11],
		});
		expect(payload.active_group_id).toBeUndefined();
	});
});
