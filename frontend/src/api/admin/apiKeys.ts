/**
 * Admin API Keys API endpoints
 * Handles API key management for administrators
 */

import type { ApiKey } from "@/types";
import { apiClient } from "../client";

export interface UpdateApiKeyGroupResult {
	api_key: ApiKey;
	auto_granted_group_access: boolean;
	granted_group_id?: number;
	granted_group_name?: string;
}

export interface UpdateApiKeyGroupsPayload {
	group_id?: number | null;
	granted_group_ids?: number[];
	default_group_id?: number | null;
}

/**
 * Update an API key's group binding
 * @param id - API Key ID
 * @param groupId - Group ID (0 to unbind, positive to bind, null/undefined to skip)
 * @returns Updated API key with auto-grant info
 */
export async function updateApiKeyGroup(
	id: number,
	groupId: number | null,
): Promise<UpdateApiKeyGroupResult> {
	const payload: UpdateApiKeyGroupsPayload = {
		group_id: groupId === null ? 0 : groupId,
	};
	const { data } = await apiClient.put<UpdateApiKeyGroupResult>(
		`/admin/api-keys/${id}`,
		payload,
	);
	return data;
}

export async function updateApiKeyGroups(
	id: number,
	payload: UpdateApiKeyGroupsPayload,
): Promise<UpdateApiKeyGroupResult> {
	const normalizedPayload: UpdateApiKeyGroupsPayload = { ...payload };

	if (normalizedPayload.group_id === null) {
		normalizedPayload.group_id = 0;
	}
	if (normalizedPayload.default_group_id === null) {
		normalizedPayload.default_group_id = 0;
	}

	const { data } = await apiClient.put<UpdateApiKeyGroupResult>(
		`/admin/api-keys/${id}`,
		normalizedPayload,
	);
	return data;
}

export const apiKeysAPI = {
	updateApiKeyGroup,
	updateApiKeyGroups,
};

export default apiKeysAPI;
