// Staff reads of members and districts shared by the Members and Rooms
// screens. Member names live only in the query data: query keys carry the
// status and district ID, never a name, and nothing here logs.
import { unwrap, type Schemas } from '@birge/api';
import { useTranslation } from '@birge/i18n';
import { useInfiniteQuery, useQuery } from '@tanstack/react-query';
import { useMemo } from 'react';

import { api } from './api';

/** StaffMember is one member as staff see her: first name and district only. */
export type StaffMember = Schemas['StaffMember'];

/** MemberStatus is the list to read: verified members or suspended ones. */
export type MemberStatus = 'verified' | 'suspended';

/** membersKey is the query key prefix of every staff members list (invalidate it after a change). */
export const membersKey = ['staff', 'members'] as const;

/**
 * useStaffMembers pages through GET /staff/members (most recent first). The
 * server audits each read, so the query runs only while enabled and is not
 * refetched on a timer.
 */
export function useStaffMembers(status: MemberStatus, districtId?: string, enabled = true) {
  return useInfiniteQuery({
    queryKey: [...membersKey, status, districtId ?? 'all'],
    queryFn: ({ pageParam }) =>
      unwrap(api.GET('/staff/members', { params: { query: { status, districtId, cursor: pageParam } } })),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.nextCursor,
    enabled,
  });
}

/** DistrictOption is a district ID with its name in the current language. */
export interface DistrictOption {
  id: string;
  label: string;
}

/**
 * useDistricts loads the deployment's districts (/reference/districts, the
 * same reference data the mom app uses) and names them from the `reference`
 * locale namespace; a code without a label is shown as is.
 */
export function useDistricts() {
  const { t } = useTranslation('reference');
  const query = useQuery({
    queryKey: ['reference', 'districts'],
    queryFn: () => unwrap(api.GET('/reference/districts')),
    staleTime: 60 * 60 * 1000,
  });
  const options = useMemo<DistrictOption[]>(
    () =>
      (query.data ?? []).flatMap((city) =>
        city.districts.map((d) => ({ id: d.id, label: t(`district.${d.code}`, { defaultValue: d.code }) })),
      ),
    [query.data, t],
  );
  const nameOf = useMemo(() => {
    const names = new Map(options.map((o) => [o.id, o.label]));
    return (id: string | undefined) => (id ? names.get(id) : undefined);
  }, [options]);
  return { options, nameOf };
}
