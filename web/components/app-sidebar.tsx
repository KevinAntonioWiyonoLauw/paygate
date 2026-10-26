"use client"

import Link from "next/link"
import {
  LayoutDashboardIcon,
  QrCodeIcon,
  ReceiptTextIcon,
  Settings2Icon,
  WalletIcon,
} from "lucide-react"

import { NavMain, type NavItem } from "@/components/nav-main"
import { NavUser } from "@/components/nav-user"
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarRail,
} from "@/components/ui/sidebar"

const navItems: NavItem[] = [
  {
    title: "Ringkasan",
    url: "/",
    icon: <LayoutDashboardIcon />,
    exact: true,
  },
  {
    title: "Order",
    url: "/orders",
    icon: <ReceiptTextIcon />,
  },
  {
    title: "Pembayaran",
    url: "/payments",
    icon: <WalletIcon />,
  },
  {
    title: "Pengaturan",
    url: "/settings",
    icon: <Settings2Icon />,
  },
]

export function AppSidebar({ username }: { username: string }) {
  return (
    <Sidebar collapsible="icon">
      <SidebarHeader>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton
              size="lg"
              tooltip="Paygate"
              render={<Link href="/" />}
            >
              <div className="flex aspect-square size-8 items-center justify-center rounded-lg bg-primary text-primary-foreground">
                <QrCodeIcon className="size-4" />
              </div>
              <div className="grid flex-1 text-left text-sm leading-tight">
                <span className="truncate font-semibold">Paygate</span>
                <span className="truncate text-xs text-muted-foreground">
                  QRIS Shopee
                </span>
              </div>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarHeader>

      <SidebarContent>
        <NavMain items={navItems} />
      </SidebarContent>

      <SidebarFooter>
        <NavUser username={username} />
      </SidebarFooter>

      <SidebarRail />
    </Sidebar>
  )
}
