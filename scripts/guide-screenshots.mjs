#!/usr/bin/env node
// docs/USER_GUIDE.md 와 docs/ADMIN_GUIDE.md 가 싣는 화면 캡처를 만든다.
//
//   VENDRA_GUIDE_URL=http://127.0.0.1:8080 \
//   VENDRA_GUIDE_ADMIN=admin@demo-vendra.example.com \
//   VENDRA_GUIDE_ADMIN_PASSWORD=… \
//   VENDRA_GUIDE_DEMO_PASSWORD=… \
//     node scripts/guide-screenshots.mjs
//
// 비밀번호는 둘 다 환경 변수로만 받는다 — 관리자 것은 로그인에, 데모 것은 이 스크립트가
// 만드는 계정(내부 사용자·포털 사용자) 전부에 쓴다. 어느 쪽도 파일에 적지 않는다.
// 데모 비밀번호는 애플리케이션의 비밀번호 정책(기본 10자 이상)을 통과해야 한다.
//
// 이 스크립트는 데모 데이터를 **만들어 넣는다**. 버리고 다시 만들 수 있는 빈
// 데이터베이스를 가리키는 배포에만 실행한다. 그래서 대상 주소는 다른 스크립트와
// 공유하지 않는 전용 변수(VENDRA_GUIDE_URL)에서만 읽고, 값이 없으면 그 자리에서
// 멈춘다. 로컬이 아닌 호스트는 VENDRA_GUIDE_ALLOW_REMOTE=1 을 함께 주지 않으면
// 거절한다.
//
// 전역 설정은 딱 하나만 건드린다 — workflow.approval_enabled. 이것이 꺼져 있으면
// 상신이 즉시 승인되어 승인함이 빈 화면으로 찍힌다. 바꾸기 전에 원래 값을 읽어
// 두고 끝나면 되돌린다. 그 밖의 정책·설정은 읽지도 쓰지도 않는다.
//
// 캡처는 headless Chrome 을 CDP 로 몰아 1440x900 으로 찍는다. 추가 의존성은
// 없다 — Node 22 의 내장 fetch 와 WebSocket 을 쓴다.
import { spawn } from 'node:child_process';
import { mkdirSync, writeFileSync, rmSync } from 'node:fs';
import { resolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const outDir = resolve(repoRoot, 'docs/images/guide');

const base = (process.env.VENDRA_GUIDE_URL || '').replace(/\/$/, '');
if (!base) {
  console.error('VENDRA_GUIDE_URL 이 없습니다. 버려도 되는 배포의 주소를 지정하세요 (예: http://127.0.0.1:8080).');
  process.exit(2);
}
const host = new URL(base).hostname;
if (!['localhost', '127.0.0.1', '::1'].includes(host) && process.env.VENDRA_GUIDE_ALLOW_REMOTE !== '1') {
  console.error(`${host} 는 로컬이 아닙니다. 이 스크립트는 데모 데이터를 만들어 넣습니다.`);
  console.error('정말 버려도 되는 배포라면 VENDRA_GUIDE_ALLOW_REMOTE=1 을 함께 지정하세요.');
  process.exit(2);
}
const adminEmail = process.env.VENDRA_GUIDE_ADMIN;
const adminPassword = process.env.VENDRA_GUIDE_ADMIN_PASSWORD;
if (!adminEmail || !adminPassword) {
  console.error('VENDRA_GUIDE_ADMIN 과 VENDRA_GUIDE_ADMIN_PASSWORD 가 필요합니다.');
  process.exit(2);
}
// 이 스크립트가 만드는 데모 계정의 비밀번호. 글자로 적지 않는다 — 비밀정보 검사에
// 걸리기도 하지만, 저장소에 적힌 비밀번호는 그 배포에 남는 계정의 비밀번호가 된다.
const DEMO_PASSWORD = process.env.VENDRA_GUIDE_DEMO_PASSWORD;
if (!DEMO_PASSWORD || DEMO_PASSWORD.length < 10) {
  console.error('VENDRA_GUIDE_DEMO_PASSWORD 가 필요합니다 (데모 계정에 쓸 비밀번호, 10자 이상).');
  process.exit(2);
}

// ---------------------------------------------------------------- 데모 데이터
// 실명·실제 주소·실제 비밀값이 화면에 남지 않도록 전부 지어낸 값이다.
// 도메인은 예약된 example.com 계열만 쓴다.

let cookie = '';
async function api(method, path, body) {
  const res = await fetch(base + path, {
    method,
    headers: { 'Content-Type': 'application/json', ...(cookie ? { Cookie: cookie } : {}) },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const setCookie = res.headers.getSetCookie?.() || [];
  for (const c of setCookie) {
    if (c.startsWith('vendra_session=')) cookie = c.split(';')[0];
  }
  const text = await res.text();
  let json;
  try { json = text ? JSON.parse(text) : null; } catch { json = text; }
  if (!res.ok) {
    const err = new Error(`${method} ${path} → ${res.status} ${text.slice(0, 300)}`);
    err.status = res.status;
    err.body = json;
    throw err;
  }
  return json;
}

// 이 스크립트가 바꾼 전역 설정과 그 원래 값. 끝나면 restoreSettings 가 되돌린다.
const restore = [];
async function restoreSettings() {
  for (const { key, value, category } of restore) {
    try {
      await api('PUT', `/api/v1/admin/settings/${key}`, { value, category });
      console.log(`· ${key} 를 원래 값으로 되돌렸습니다`);
    } catch (e) {
      console.error(`!! ${key} 를 되돌리지 못했습니다 — 직접 확인하세요: ${e.message.slice(0, 160)}`);
    }
  }
  restore.length = 0;
}

const today = new Date();
const day = (offset) => {
  const d = new Date(today);
  d.setDate(d.getDate() + offset);
  return d.toISOString().slice(0, 10);
};

const SUPPLIERS = [
  {
    name: '한빛정밀', legalName: '주식회사 한빛정밀', businessNumber: '110-81-00011',
    representative: '김하늘', industry: '기계가공', supplierType: 'manufacturer',
    categories: ['가공품', '금형'], status: 'active', grade: 'A', riskLevel: 'LOW',
    phone: '02-3456-1101', email: 'sales@hanbit.example.com', website: 'hanbit.example.com',
    tradingSince: '2019-03-04', annualSpend: 1840000000,
    addresses: [{ type: 'head', address: '경기도 화성시 동탄대로 101' }],
  },
  {
    name: '대성전자', legalName: '대성전자 주식회사', businessNumber: '110-81-00022',
    representative: '박서준', industry: '전자부품', supplierType: 'manufacturer',
    categories: ['전자부품', 'PCB'], status: 'active', grade: 'B', riskLevel: 'MEDIUM',
    phone: '031-777-2202', email: 'contact@daesung.example.com', website: 'daesung.example.com',
    tradingSince: '2021-06-15', annualSpend: 920000000,
    addresses: [{ type: 'head', address: '인천광역시 남동구 논현로 22' }],
  },
  {
    name: '누리소재', legalName: '누리소재 주식회사', businessNumber: '110-81-00033',
    representative: '이도윤', industry: '화학소재', supplierType: 'manufacturer',
    categories: ['원자재', '수지'], status: 'active', grade: 'B', riskLevel: 'HIGH',
    phone: '052-330-3303', email: 'help@nuri.example.com', website: 'nuri.example.com',
    tradingSince: '2020-11-02', annualSpend: 610000000,
    addresses: [{ type: 'head', address: '울산광역시 남구 산업로 303' }],
  },
  {
    name: '가온물류', legalName: '가온물류 주식회사', businessNumber: '110-81-00044',
    representative: '최지우', industry: '물류', supplierType: 'service',
    categories: ['운송', '보관'], status: 'active', grade: 'A', riskLevel: 'LOW',
    phone: '02-555-4404', email: 'ops@gaon.example.com', website: 'gaon.example.com',
    tradingSince: '2018-01-08', annualSpend: 430000000,
    addresses: [{ type: 'head', address: '서울특별시 강서구 마곡중앙로 44' }],
  },
  {
    name: '세움엔지니어링', legalName: '세움엔지니어링 주식회사', businessNumber: '110-81-00055',
    representative: '정민서', industry: '설비', supplierType: 'service',
    categories: ['설비공사', '유지보수'], status: 'candidate', grade: 'C', riskLevel: 'MEDIUM',
    phone: '042-880-5505', email: 'info@seum.example.com', website: 'seum.example.com',
    tradingSince: '2024-02-19', annualSpend: 180000000,
    addresses: [{ type: 'head', address: '대전광역시 유성구 테크노로 55' }],
  },
  {
    name: '푸른포장', legalName: '푸른포장 주식회사', businessNumber: '110-81-00066',
    representative: '한소율', industry: '포장재', supplierType: 'manufacturer',
    categories: ['포장재'], status: 'suspended', grade: 'C', riskLevel: 'HIGH',
    phone: '063-210-6606', email: 'sales@pureun.example.com', website: 'pureun.example.com',
    tradingSince: '2022-09-30', annualSpend: 95000000,
    addresses: [{ type: 'head', address: '전라북도 익산시 산단로 66' }],
  },
];

async function seed() {
  console.log('· 로그인');
  await api('POST', '/api/auth/login', { email: adminEmail, password: adminPassword });

  const existing = await api('GET', '/api/v1/suppliers?limit=5');
  if ((existing.items || []).length > 0) {
    // 이미 채워진 배포에는 아무것도 덧쓰지 않는다. 상세 화면을 찍으려면 어떤
    // 레코드를 열지는 알아야 하므로, 있는 것 중에서 고른다.
    console.log('· 이미 데이터가 있는 배포입니다 — 데모 데이터를 추가하지 않고 캡처만 합니다.');
    const rfqs = await api('GET', '/api/v1/rfq?limit=5').catch(() => ({ items: [] }));
    const users = await api('GET', '/api/v1/admin/users?limit=200').catch(() => ({ items: [] }));
    const portal = (users.items || []).find((u) => u.userType === 'supplier' && u.status === 'active');
    // 상세 화면은 포털 계정이 묶인 업체로 연다 — 계약·발주·납품이 붙어 있는 쪽이다.
    const ids = existing.items.map((s) => s.id);
    if (portal?.supplierId) ids.unshift(portal.supplierId);
    return {
      seeded: false,
      supplierIds: ids,
      rfqId: (rfqs.items || [])[0]?.id,
      portalUser: portal ? { email: portal.email, id: portal.id } : null,
    };
  }

  console.log('· 조직');
  const orgs = {};
  for (const name of ['구매본부', '품질본부', '생산본부']) {
    const created = await api('POST', '/api/v1/admin/organizations', { name });
    orgs[name] = created.id;
  }

  console.log('· 사용자');
  const people = [
    { email: 'buyer@demo-vendra.example.com', displayName: '구매담당 강나윤', roleCodes: ['procurement_manager'], organizationId: orgs['구매본부'] },
    { email: 'contract@demo-vendra.example.com', displayName: '계약담당 오세진', roleCodes: ['contract_manager'], organizationId: orgs['구매본부'] },
    { email: 'quality@demo-vendra.example.com', displayName: '품질담당 윤채원', roleCodes: ['business_user'], organizationId: orgs['품질본부'] },
  ];
  const userIds = {};
  for (const person of people) {
    try {
      const created = await api('POST', '/api/v1/admin/users', { ...person, userType: 'internal', status: 'active', password: DEMO_PASSWORD });
      userIds[person.email] = created.id;
    } catch (e) {
      if (e.status !== 409) throw e;
    }
  }

  console.log('· 공급업체');
  const supplierIds = [];
  for (const s of SUPPLIERS) {
    const created = await api('POST', '/api/v1/suppliers', { ...s, organizationId: orgs['구매본부'] });
    supplierIds.push(created.id || created.supplier?.id);
  }

  console.log('· 담당자 연락처');
  const contacts = [
    ['영업 담당 서지호', 'jiho@hanbit.example.com', '02-3456-1102'],
    ['영업 담당 문가은', 'gaeun@daesung.example.com', '031-777-2203'],
  ];
  for (let i = 0; i < contacts.length; i += 1) {
    const [name, email, phone] = contacts[i];
    await api('POST', `/api/v1/suppliers/${supplierIds[i]}/contacts`, {
      name, email, phone, position: '차장', department: '영업팀', primary: true,
    });
  }

  console.log('· 평가');
  const scoreSets = [
    { price: 88, quality: 94, delivery: 91, service: 86, technology: 90, security: 82, finance: 88, esg: 79 },
    { price: 76, quality: 81, delivery: 74, service: 78, technology: 80, security: 71, finance: 77, esg: 68 },
    { price: 71, quality: 69, delivery: 66, service: 72, technology: 70, security: 64, finance: 62, esg: 60 },
    { price: 90, quality: 89, delivery: 95, service: 92, technology: 84, security: 80, finance: 86, esg: 83 },
  ];
  for (let i = 0; i < scoreSets.length; i += 1) {
    await api('POST', `/api/v1/suppliers/${supplierIds[i]}/evaluations`, {
      evaluationType: 'periodic', status: 'completed',
      periodStart: day(-180), periodEnd: day(-1), scores: scoreSets[i],
      comments: '반기 정기 평가 결과입니다.',
    });
  }

  console.log('· 리스크');
  const risks = [
    [2, 'financial', 'HIGH', 7, 8, '부채비율이 2분기 연속 상승했습니다.', '분기별 재무제표 제출과 대체 공급처 확보'],
    [1, 'delivery', 'MEDIUM', 5, 6, '단일 라인 의존으로 납기 변동 위험이 있습니다.', '2공장 이원화 계획 요청'],
    [5, 'compliance', 'HIGH', 6, 7, '환경 인허가 갱신 기한이 임박했습니다.', '갱신 증빙 제출 요청'],
    [0, 'quality', 'LOW', 2, 3, '경미한 치수 편차가 반복 보고되었습니다.', '수입검사 항목 추가'],
  ];
  for (const [idx, riskType, severity, probability, impact, description, mitigation] of risks) {
    await api('POST', `/api/v1/suppliers/${supplierIds[idx]}/risks`, {
      riskType, severity, probability, impact, description, mitigation, status: 'open', reviewDate: day(30),
    });
  }

  console.log('· 업무 객체');
  const objects = [
    ['/api/v1/contracts', { supplierId: supplierIds[0], title: '2026년 정밀가공품 단가계약', status: 'active', amount: 680000000, currency: 'KRW', startDate: day(-120), endDate: day(245), riskLevel: 'LOW', data: { contractType: 'unit_price', category: '가공품' } }],
    ['/api/v1/contracts', { supplierId: supplierIds[1], title: 'PCB 모듈 공급계약', status: 'active', amount: 320000000, currency: 'KRW', startDate: day(-60), endDate: day(305), riskLevel: 'MEDIUM', data: { contractType: 'supply', category: '전자부품' } }],
    ['/api/v1/contracts', { supplierId: supplierIds[3], title: '수도권 운송 위탁계약', status: 'active', amount: 150000000, currency: 'KRW', startDate: day(-200), endDate: day(30), riskLevel: 'LOW', data: { contractType: 'service', category: '운송' } }],
    ['/api/v1/purchase-requests', { supplierId: supplierIds[0], title: '3분기 하우징 가공품 구매요청', status: 'draft', amount: 84000000, dueDate: day(14), data: { category: '가공품', project: 'K-라인 증설' } }],
    ['/api/v1/purchase-requests', { title: '검사장비 교체 구매요청', status: 'draft', amount: 47000000, dueDate: day(21), data: { category: '설비', project: '품질검사 고도화' } }],
    ['/api/v1/rfq', { title: '하우징 가공품 견적요청 (3사)', status: 'in_progress', amount: 84000000, currency: 'KRW', dueDate: day(7), data: { category: '가공품' } }],
    ['/api/v1/rfp', { title: '물류창고 자동화 제안요청', status: 'in_progress', amount: 250000000, currency: 'KRW', dueDate: day(20), data: { category: '설비공사' } }],
    ['/api/v1/purchase-orders', { supplierId: supplierIds[0], title: 'PO-하우징 가공품 1차', status: 'confirmed', amount: 42000000, dueDate: day(9), data: { category: '가공품', quantity: 1200 } }],
    ['/api/v1/purchase-orders', { supplierId: supplierIds[1], title: 'PO-PCB 모듈 8월분', status: 'confirmed', amount: 26500000, dueDate: day(4), data: { category: '전자부품', quantity: 500 } }],
    ['/api/v1/purchase-orders', { supplierId: supplierIds[3], title: 'PO-8월 운송비', status: 'draft', amount: 12500000, dueDate: day(12), data: { category: '운송' } }],
    ['/api/v1/deliveries', { supplierId: supplierIds[0], title: '하우징 가공품 1차 납품', status: 'completed', amount: 42000000, dueDate: day(-3), data: { quantity: 1200 } }],
    ['/api/v1/deliveries', { supplierId: supplierIds[1], title: 'PCB 모듈 8월 1차 납품', status: 'in_progress', amount: 13200000, dueDate: day(2), data: { quantity: 250 } }],
    ['/api/v1/inspections', { supplierId: supplierIds[0], title: '하우징 가공품 1차 수입검사', status: 'accepted', dueDate: day(-2), score: 96, data: { defects: 2, lotSize: 1200 } }],
    ['/api/v1/quality', { supplierId: supplierIds[2], title: '수지 색상 편차 NCR', status: 'ncr', dueDate: day(6), riskLevel: 'MEDIUM', data: { defectType: '외관', lotNumber: 'LOT-2609-A' } }],
    ['/api/v1/issues', { supplierId: supplierIds[1], title: '8월 납기 지연 통보', status: 'open', dueDate: day(3), riskLevel: 'MEDIUM', data: { category: '납기' } }],
    ['/api/v1/invoices', { supplierId: supplierIds[0], title: '하우징 가공품 1차 세금계산서', status: 'submitted', amount: 42000000, dueDate: day(18), data: { taxInvoiceNumber: 'TI-2609-0001' } }],
    ['/api/v1/payments', { supplierId: supplierIds[3], title: '7월 운송비 지급', status: 'draft', amount: 11800000, dueDate: day(5), data: { term: 'NET30' } }],
  ];
  const created = [];
  for (const [path, body] of objects) {
    created.push({ path, object: await api('POST', path, body) });
  }

  console.log('· Spend 거래');
  const spendRows = [
    [0, '정밀 하우징', '가공품', 320, 'EA', 35000, 11200000, day(-40)],
    [0, '가공 브래킷', '가공품', 800, 'EA', 12000, 9600000, day(-25)],
    [1, 'PCB 모듈 A', '전자부품', 250, 'EA', 52800, 13200000, day(-18)],
    [1, '커넥터 세트', '전자부품', 1500, 'EA', 3400, 5100000, day(-12)],
    [2, '엔지니어링 수지', '원자재', 6000, 'KG', 1450, 8700000, day(-30)],
    [3, '수도권 운송', '운송', 1, 'LOT', 12500000, 12500000, day(-8)],
    [3, '보관료 8월', '보관', 1, 'LOT', 4200000, 4200000, day(-6)],
    [4, '설비 정기점검', '유지보수', 1, 'LOT', 6800000, 6800000, day(-15)],
  ];
  for (const [idx, itemName, category, quantity, unit, unitPrice, amount, transactionDate] of spendRows) {
    await api('POST', '/api/v1/spend/transactions', {
      supplierId: supplierIds[idx], itemName, category, quantity, unit, unitPrice,
      amount, currency: 'KRW', transactionDate, contracted: true, paymentStatus: 'paid',
    });
  }

  console.log('· 공급망 관계');
  await api('POST', '/api/v1/supplier-network/relationships', {
    sourceSupplierId: supplierIds[0], targetSupplierId: supplierIds[2],
    relationshipType: 'subcontractor', criticality: 'high',
    categories: ['원자재'], dependencyPercent: 45, notes: '수지 원자재 2차 공급',
  }).catch(() => {});
  await api('POST', '/api/v1/supplier-network/relationships', {
    sourceSupplierId: supplierIds[1], targetSupplierId: supplierIds[3],
    relationshipType: 'logistics', criticality: 'medium',
    categories: ['운송'], dependencyPercent: 70, notes: '완제품 운송 위탁',
  }).catch(() => {});

  console.log('· RFQ 참가업체와 견적');
  const rfq = created.find((c) => c.path === '/api/v1/rfq')?.object;
  let portalUser = null;
  if (rfq?.id) {
    await api('POST', `/api/v1/sourcing/${rfq.id}/participants`, {
      supplierIds: supplierIds.slice(0, 3),
    }).catch((e) => console.log('  참가업체 등록 건너뜀:', e.message.slice(0, 120)));
    await api('POST', `/api/v1/sourcing/${rfq.id}/questions`, {
      question: '도면 REV-C 기준으로 견적을 내면 되는지 확인 부탁드립니다.', visibility: 'all',
    }).catch(() => {});
  }

  console.log('· 승인 규칙');
  // 결재 규칙이 없으면 상신은 곧바로 승인되고 승인함이 빈 화면으로 찍힌다.
  const workflows = [
    {
      name: '구매요청 승인 (3천만원 이상)', objectType: 'purchase_request', enabled: true,
      conditions: { minAmount: 30000000 },
      steps: [{ name: '구매 관리자 승인', role: 'procurement_manager' }, { name: '시스템 관리자 승인', role: 'system_admin' }],
    },
    {
      name: '계약 체결 승인', objectType: 'contract', enabled: true,
      conditions: { minAmount: 100000000 },
      steps: [{ name: '계약 담당자 검토', role: 'contract_manager' }, { name: '시스템 관리자 승인', role: 'system_admin' }],
    },
    {
      name: 'Invoice 지급 승인', objectType: 'invoice', enabled: true, conditions: {},
      steps: [{ name: '시스템 관리자 승인', role: 'system_admin' }],
    },
    {
      name: '지급 실행 승인', objectType: 'payment', enabled: true, conditions: {},
      steps: [{ name: '시스템 관리자 승인', role: 'system_admin' }],
    },
  ];
  for (const definition of workflows) {
    await api('POST', '/api/v1/workflows', definition)
      .catch((e) => console.log('  승인 규칙 건너뜀:', e.message.slice(0, 140)));
  }

  console.log('· 승인 요청');
  // workflow.approval_enabled 가 꺼져 있으면 상신이 그 자리에서 승인된다. 원래
  // 값을 적어 두고 켠 뒤, 캡처가 끝나면 restoreSettings 가 되돌린다.
  const settings = await api('GET', '/api/v1/admin/settings');
  const approvalSetting = (settings.items || []).find((s) => s.key === 'workflow.approval_enabled');
  if (approvalSetting && approvalSetting.value !== true) {
    restore.push({ key: 'workflow.approval_enabled', value: approvalSetting.value, category: approvalSetting.category });
    await api('PUT', '/api/v1/admin/settings/workflow.approval_enabled', {
      value: true, category: approvalSetting.category,
    });
  }
  for (const path of ['/api/v1/purchase-requests', '/api/v1/invoices', '/api/v1/payments']) {
    const target = created.find((c) => c.path === path)?.object;
    if (!target?.id) continue;
    await api('POST', `${path}/${target.id}/submit`, {}).catch((e) =>
      console.log('  상신 건너뜀:', e.message.slice(0, 140)));
  }

  console.log('· 공급업체 포털 계정');
  const portalAccounts = [
    ['portal@hanbit.example.com', '한빛정밀 서지호', 0],
    ['portal@daesung.example.com', '대성전자 문가은', 1],
    ['portal@nuri.example.com', '누리소재 배주원', 2],
  ];
  for (const [email, displayName, idx] of portalAccounts) {
    try {
      const account = await api('POST', '/api/v1/admin/users', {
        email, displayName, userType: 'supplier', status: 'active',
        password: DEMO_PASSWORD, supplierId: supplierIds[idx], roleCodes: ['supplier_user'],
      });
      if (idx === 0) portalUser = { email, id: account.id };
    } catch (e) {
      if (e.status !== 409) console.log('  포털 계정 건너뜀:', e.message.slice(0, 120));
    }
  }

  // 비교표는 견적이 들어와야 표가 된다 — 초대받은 세 곳이 각자 자기 계정으로 낸다.
  if (rfq?.id) {
    console.log('· 포털에서 견적 제출');
    const bids = [
      [0, 78400000, 21, '18개월', '도면 REV-C 기준, 표면처리 포함'],
      [1, 81200000, 14, '12개월', '치공구 신규 제작비 별도 협의'],
      [2, 86500000, 28, '24개월', '원자재 시황 연동 조건'],
    ];
    const adminCookie = cookie;
    for (const [idx, totalAmount, deliveryDays, warranty, note] of bids) {
      try {
        cookie = '';
        await api('POST', '/api/auth/login', { email: portalAccounts[idx][0], password: DEMO_PASSWORD });
        await api('PUT', `/api/v1/portal/sourcing/${rfq.id}/response`, {
          submit: true, currency: 'KRW', totalAmount, deliveryDays, warranty,
          validityDate: day(30), commercialTerms: { note, payment: 'NET30' },
        });
      } catch (e) {
        console.log('  견적 제출 건너뜀:', e.message.slice(0, 140));
      }
    }
    cookie = adminCookie;
  }

  return { seeded: true, supplierIds, rfqId: rfq?.id, portalUser };
}

// ------------------------------------------------------------------ 캡처 (CDP)
let chromeProcess = null;
let ws = null;
let nextId = 1;
const pending = new Map();

function send(method, params = {}, sessionId) {
  const id = nextId++;
  return new Promise((resolvePromise, rejectPromise) => {
    pending.set(id, { resolvePromise, rejectPromise });
    ws.send(JSON.stringify({ id, method, params, ...(sessionId ? { sessionId } : {}) }));
  });
}

async function startChrome() {
  const profile = mkdtempSync(resolve(tmpdir(), 'vendra-guide-chrome-'));
  const binary = ['google-chrome', 'google-chrome-stable', 'chromium', 'chromium-browser'].find((c) => {
    try { spawn(c, ['--version']).kill(); return true; } catch { return false; }
  }) || 'google-chrome';
  chromeProcess = spawn(binary, [
    '--headless=new', '--no-sandbox', '--disable-gpu', '--hide-scrollbars',
    '--force-device-scale-factor=1', '--window-size=1440,900',
    '--remote-debugging-port=9333', `--user-data-dir=${profile}`,
    '--disable-dev-shm-usage', 'about:blank',
  ], { stdio: 'ignore' });
  chromeProcess.profile = profile;

  for (let attempt = 0; attempt < 60; attempt += 1) {
    try {
      const res = await fetch('http://127.0.0.1:9333/json/version');
      const info = await res.json();
      ws = new WebSocket(info.webSocketDebuggerUrl);
      await new Promise((ok, fail) => { ws.onopen = ok; ws.onerror = fail; });
      ws.onmessage = (event) => {
        const msg = JSON.parse(event.data);
        if (msg.id && pending.has(msg.id)) {
          const { resolvePromise, rejectPromise } = pending.get(msg.id);
          pending.delete(msg.id);
          msg.error ? rejectPromise(new Error(JSON.stringify(msg.error))) : resolvePromise(msg.result);
        }
      };
      return;
    } catch {
      await new Promise((ok) => setTimeout(ok, 250));
    }
  }
  throw new Error('Chrome 을 띄우지 못했습니다.');
}

let sessionId = null;
async function openPage() {
  const { targetId } = await send('Target.createTarget', { url: 'about:blank' });
  ({ sessionId } = await send('Target.attachToTarget', { targetId, flatten: true }));
  await send('Page.enable', {}, sessionId);
  await send('Runtime.enable', {}, sessionId);
  await send('Emulation.setDeviceMetricsOverride', {
    width: 1440, height: 900, deviceScaleFactor: 1, mobile: false,
  }, sessionId);
}

async function evaluate(expression) {
  const result = await send('Runtime.evaluate', {
    expression, awaitPromise: true, returnByValue: true,
  }, sessionId);
  if (result.exceptionDetails) throw new Error(result.exceptionDetails.text + ' ' + expression.slice(0, 80));
  return result.result.value;
}

async function goto(path) {
  await send('Page.navigate', { url: base + path }, sessionId);
  // 로딩이 끝난 뒤에 찍는다 — 스피너가 박제된 캡처는 다시 찍는다.
  for (let attempt = 0; attempt < 80; attempt += 1) {
    await new Promise((ok) => setTimeout(ok, 250));
    const settled = await evaluate(`(() => {
      if (document.readyState !== 'complete') return false;
      if (!document.querySelector('#root')?.children.length) return false;
      const text = document.body.innerText || '';
      if (/불러오는 중|로딩 중/.test(text)) return false;
      return true;
    })()`).catch(() => false);
    if (settled) break;
  }
  await new Promise((ok) => setTimeout(ok, 900));
}

async function login(email, password) {
  await send('Page.navigate', { url: base + '/' }, sessionId);
  await new Promise((ok) => setTimeout(ok, 700));
  await evaluate(`fetch('/api/auth/logout', { method: 'POST' }).catch(() => {})`);
  const ok = await evaluate(`fetch('/api/auth/login', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: ${JSON.stringify(JSON.stringify({ email, password }))}
  }).then(r => r.status)`);
  if (ok !== 200) throw new Error(`${email} 로그인 실패 (${ok})`);
}

async function shot(name) {
  const { data } = await send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false }, sessionId);
  writeFileSync(resolve(outDir, `${name}.png`), Buffer.from(data, 'base64'));
  console.log(`  ✓ ${name}.png`);
}

const USER_SCREENS = [
  ['dashboard', '/'],
  ['suppliers', '/suppliers'],
  // 검색은 결과가 보이는 상태로 찍는다 — 빈 검색창은 화면을 설명하지 못한다.
  ['search', '/search?q=한빛'],
  ['evaluations', '/evaluations'],
  ['risks', '/risks'],
  ['purchase-requests', '/purchase-requests'],
  ['rfq', '/rfq'],
  ['contracts', '/contracts'],
  ['purchase-orders', '/purchase-orders'],
  ['deliveries', '/deliveries'],
  ['quality', '/quality'],
  ['invoices', '/invoices'],
  ['spend', '/spend'],
  ['network', '/network'],
  ['work-inbox', '/work'],
  ['approvals', '/approvals'],
  ['ai-analyst', '/ai'],
  ['profile', '/profile'],
];

const ADMIN_SCREENS = [
  ['admin-settings', '/admin'],
  ['admin-users', '/admin/users'],
  ['admin-workflow', '/admin/workflow'],
  ['admin-scorecard', '/admin/scorecard'],
  ['admin-lifecycle', '/admin/lifecycle'],
  ['admin-audit', '/admin/audit'],
  ['admin-logs', '/admin/logs'],
];

async function main() {
  const state = await seed();
  mkdirSync(outDir, { recursive: true });
  await startChrome();
  await openPage();

  console.log('· 로그인 화면');
  await goto('/');
  await evaluate(`fetch('/api/auth/logout', { method: 'POST' })`);
  await goto('/');
  await shot('login');

  console.log('· 사용자 화면');
  await login(adminEmail, adminPassword);
  for (const [name, path] of USER_SCREENS) {
    await goto(path);
    await shot(name);
  }

  if (state.supplierIds?.length) {
    await goto(`/suppliers/${state.supplierIds[0]}`);
    await shot('supplier-detail');
  }
  if (state.rfqId) {
    await goto(`/sourcing/rfq/${state.rfqId}`);
    await shot('sourcing');
  }

  console.log('· 관리자 화면');
  for (const [name, path] of ADMIN_SCREENS) {
    await goto(path);
    await shot(name);
  }

  if (state.portalUser) {
    // 포털 계정의 비밀번호를 모르는 배포에서는 이 한 장만 빠진다.
    try {
      console.log('· 공급업체 포털');
      await login(state.portalUser.email, DEMO_PASSWORD);
      await goto('/');
      await shot('portal');
      // 포털은 해시로 절을 고른다 (Portal.tsx 의 portalSections).
      await goto('/#rfq');
      await shot('portal-rfq');
    } catch (e) {
      console.log('  포털 캡처 건너뜀:', e.message.slice(0, 160));
    }
  }

  await restoreSettings();
  console.log(`\n캡처 완료 → ${outDir}`);
}

main()
  // 도중에 실패해도 바꾼 설정은 되돌린다 — 그러지 않으면 다음 사람이 켜 둔
  // 승인 절차를 물려받는다.
  .catch(async (e) => { console.error(e); process.exitCode = 1; await restoreSettings(); })
  .finally(() => {
    try { ws?.close(); } catch {}
    if (chromeProcess) {
      chromeProcess.kill();
      try { rmSync(chromeProcess.profile, { recursive: true, force: true }); } catch {}
    }
  });
